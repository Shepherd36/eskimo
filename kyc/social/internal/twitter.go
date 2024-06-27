// SPDX-License-Identifier: ice License 1.0

package social

import (
	"bytes"
	"context"
	"encoding/json"
	"math/rand"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"github.com/hashicorp/go-multierror"
	"github.com/imroc/req/v3"
	"github.com/pkg/errors"

	"github.com/ice-blockchain/wintr/log"
	"github.com/ice-blockchain/wintr/time"
)

func (t *twitterVerifierImpl) VerifyText(ctx context.Context, doc *goquery.Document, expectedText string) (found bool) {
	isURL := strings.HasPrefix(expectedText, "http://") || strings.HasPrefix(expectedText, "https://")
	if isURL {
		return t.VerifyPostLink(ctx, doc, expectedText)
	}

	doc.Find("p").EachWithBreak(func(_ int, s *goquery.Selection) bool {
		found = found || strings.Contains(s.Text(), strings.TrimSpace(expectedText))

		return !found
	})

	return
}

func (t *twitterVerifierImpl) VerifyPostLinkOf(ctx context.Context, target, expectedURL string) bool {
	if strings.EqualFold(target, expectedURL) {
		return true
	}

	if strings.HasPrefix(target, "https://t.co") {
		loc, err := t.Scraper.Fetcher().Head(ctx, target)
		if err != nil {
			log.Warn("twitter: failed to fetch location header", "error", err)
			// Fallthrough.
		} else if strings.EqualFold(loc, expectedURL) {
			return true
		}
	}

	result, err := t.Scrape(ctx, target)
	if err != nil {
		log.Warn("twitter: failed to scrape", "error", err)

		return false
	}

	return strings.Contains(strings.ToLower(string(result.Content)), strings.ToLower(expectedURL))
}

func (t *twitterVerifierImpl) VerifyPostLink(ctx context.Context, doc *goquery.Document, expectedPostURL string) (foundPost bool) {
	doc.Find("a").EachWithBreak(func(_ int, s *goquery.Selection) bool {
		for _, node := range s.Nodes {
			for attrIndex := range node.Attr {
				if node.Attr[attrIndex].Key == "href" {
					foundPost = t.VerifyPostLinkOf(ctx, node.Attr[attrIndex].Val, expectedPostURL)
				}
			}
		}

		return !foundPost
	})

	return
}

func (t *twitterVerifierImpl) VerifyContent(ctx context.Context, oe *twitterOE, meta *Metadata) (err error) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader([]byte(oe.HTML)))
	if err != nil {
		return multierror.Append(ErrInvalidPageContent, err)
	}

	if meta.ExpectedPostText != "" && !t.VerifyText(ctx, doc, meta.ExpectedPostText) {
		return ErrTextNotFound
	}

	if meta.ExpectedPostURL != "" && !t.VerifyPostLink(ctx, doc, meta.ExpectedPostURL) {
		return ErrPostNotFound
	}

	return nil
}

func (*twitterVerifierImpl) ExtractUsernameFromURL(postURL string) (username string, err error) {
	const (
		expectedTokensLenMin  = 5
		expectedUsernameIndex = 3
		expectedStatusIndex   = 4
		expectedStatusText    = "status"
	)

	if tokens := strings.Split(postURL, "/"); len(tokens) > expectedTokensLenMin && //nolint:revive // False-Positive.
		tokens[expectedStatusIndex] == expectedStatusText {
		username = tokens[expectedUsernameIndex]
	}

	if username == "" {
		err = errors.Wrap(ErrUsernameNotFound, postURL)
	}

	return
}

func twitterRetryFn(resp *req.Response, err error) bool {
	if err != nil {
		return true
	}

	switch resp.GetStatusCode() {
	case http.StatusOK, http.StatusForbidden:
		return false

	default:
		return true
	}
}

func (t *twitterVerifierImpl) Scrape(ctx context.Context, target string) (result *webScraperResult, err error) { //nolint:funlen // .
	for _, country := range t.countries() {
		if result, err = t.Scraper.Scrape(ctx, target,
			webScraperOptions{
				Retry: twitterRetryFn,
				ProxyOptions: func(m map[string]string) map[string]string {
					m["country"] = country
					delete(m, "render_js")
					delete(m, "wait_until")

					return m
				},
			}); err == nil {
			break
		}
	}
	if err != nil {
		return nil, multierror.Append(ErrFetchFailed, err)
	}

	switch result.Code {
	case http.StatusOK:
		return result, nil

	case http.StatusForbidden:
		const errorText = `Sorry, you are not authorized to see this status.`

		if strings.Contains(string(result.Content), errorText) {
			return nil, ErrTweetPrivate
		}

		fallthrough

	default:
		return nil, multierror.Append(ErrFetchFailed, errors.Errorf("unexpected status code: `%v`, response: `%v`", result.Code, string(result.Content)))
	}
}

func (t *twitterVerifierImpl) FetchOE(ctx context.Context, postURL string) (*twitterOE, error) {
	var (
		result *webScraperResult
		err    error
	)

	target := url.URL{
		Scheme:   "https",
		Host:     "publish.twitter.com",
		Path:     "/oembed",
		RawQuery: url.Values{"url": {postURL}}.Encode(),
	}

	if result, err = t.Scrape(ctx, target.String()); err != nil {
		return nil, err
	}

	var oe twitterOE
	if err = json.Unmarshal(result.Content, &oe); err != nil {
		return nil, multierror.Append(ErrInvalidPageContent, err)
	} else if oe.HTML == "" {
		return nil, errors.Wrap(ErrInvalidPageContent, "empty page")
	}

	return &oe, nil
}

func (*twitterVerifierImpl) remapDomain(postURL string) (string, error) {
	parsed, err := url.Parse(postURL)
	if err != nil {
		return "", errors.Wrap(ErrInvalidURL, postURL)
	}

	parsed.Host = "twitter.com"

	return parsed.String(), nil
}

func (t *twitterVerifierImpl) VerifyPost(ctx context.Context, meta *Metadata) (username string, err error) {
	validDomain := false
	for i := range t.Domains {
		validDomain = validDomain || hasRootDomainAndHTTPS(meta.PostURL, t.Domains[i])
	}
	if !validDomain {
		return "", errors.Wrap(ErrInvalidURL, meta.PostURL)
	}

	if meta.PostURL, err = t.remapDomain(meta.PostURL); err != nil {
		return "", err
	}

	username, err = t.ExtractUsernameFromURL(meta.PostURL)
	if username == "" {
		return "", err
	}

	oe, err := t.FetchOE(ctx, meta.PostURL)
	if err != nil {
		return username, err
	}

	return username, t.VerifyContent(ctx, oe, meta)
}

func (t *twitterVerifierImpl) countries() []string {
	countries := slices.Clone(t.Countries)
	rand.New(rand.NewSource(time.Now().UnixNano())).Shuffle(len(countries), func(ii, jj int) { //nolint:gosec // .
		countries[ii], countries[jj] = countries[jj], countries[ii]
	})

	return removeDuplicates(countries)
}

func removeDuplicates(strSlice []string) []string {
	allKeys := make(map[string]bool, len(strSlice))
	list := make([]string, 0, len(strSlice))
	for _, item := range strSlice {
		if _, value := allKeys[item]; !value {
			allKeys[item] = true
			list = append(list, item)
		}
	}

	return list
}

func newTwitterVerifier(sc webScraper, allowedDomains, countries []string) *twitterVerifierImpl {
	return &twitterVerifierImpl{
		Scraper:   sc,
		Domains:   allowedDomains,
		Countries: countries,
	}
}
