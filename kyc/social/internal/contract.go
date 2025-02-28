// SPDX-License-Identifier: ice License 1.0

package social

import (
	"context"

	"github.com/imroc/req/v3"
	"github.com/pkg/errors"
)

// Private API.

const (
	applicationYAMLKey = "kyc/social"
)

type (
	StrategyType string

	Metadata struct {
		AccessToken      string
		PostURL          string
		ExpectedPostText string
		ExpectedPostURL  string
	}

	Verifier interface {
		VerifyPost(ctx context.Context, metadata *Metadata) (username string, err error)
	}
)

type (
	webScraperOptions struct {
		Retry        req.RetryConditionFunc
		ProxyOptions func(map[string]string) map[string]string
	}

	webScraperResult struct {
		Content []byte
		Code    int
	}

	webScraper interface {
		Scrape(ctx context.Context, url string, opts webScraperOptions) (result *webScraperResult, err error)
		Fetcher() dataFetcher
	}

	dataFetcher interface {
		Fetch(ctx context.Context, url string, retry req.RetryConditionFunc) (content []byte, httpCode int, err error)
		Head(ctx context.Context, url string) (location string, err error)
	}

	censorer interface {
		Censor(in error) (out error)
	}

	webScraperImpl struct {
		DataFetcher  dataFetcher
		ScrapeAPIURL string
		APIKey       string
	}

	dataFetcherImpl struct {
		Censorer censorer
	}

	censorerImpl struct {
		Strings []string
	}

	twitterVerifierImpl struct {
		Scraper   webScraper
		Domains   []string
		Countries []string
	}

	twitterOE struct {
		HTML string `json:"html"`
	}

	facebookVerifierImpl struct {
		Fetcher             dataFetcher
		AppID               string
		AppSecret           string
		Post                string
		AllowLongLiveTokens bool
	}

	configTwitter struct {
		Domains   []string `yaml:"domains"  mapstructure:"domains"`
		Countries []string `yaml:"countries"  mapstructure:"countries"`
	}

	configFacebook struct {
		AppID               string `yaml:"app-id"     mapstructure:"app-id"`                             //nolint:tagliatelle // Nope.
		AppSecret           string `yaml:"app-secret" mapstructure:"app-secret"`                         //nolint:tagliatelle // Nope.
		AllowLongLiveTokens bool   `yaml:"allow-long-live-tokens" mapstructure:"allow-long-live-tokens"` //nolint:tagliatelle // Nope.
	}

	config struct {
		WebScrapingAPI struct {
			APIKey string `yaml:"api-key" mapstructure:"api-key"` //nolint:tagliatelle // Nope.
			URL    string `yaml:"url"     mapstructure:"url"`
		} `yaml:"web-scraping-api" mapstructure:"web-scraping-api"` //nolint:tagliatelle // Nope.

		SocialLinks struct {
			Facebook configFacebook `yaml:"facebook" mapstructure:"facebook"`
			Twitter  configTwitter  `yaml:"twitter"  mapstructure:"twitter"`
		} `yaml:"social-links" mapstructure:"social-links"` //nolint:tagliatelle // Nope.
	}

	facebookTokenResponse struct {
		Data struct {
			AppID    string   `json:"app_id"`  //nolint:tagliatelle // Nope.
			UserID   string   `json:"user_id"` //nolint:tagliatelle // Nope.
			Scopes   []string `json:"scopes"`
			IssuedAt int64    `json:"issued_at"` //nolint:tagliatelle // Nope.
			Valid    bool     `json:"is_valid"`  //nolint:tagliatelle // Nope.
		} `json:"data"`
	}

	facebookFeedResponse struct {
		Paging struct {
			Next     string `json:"next"`
			Previous string `json:"previous"`
		} `json:"paging"`
		Data []struct {
			Message     string `json:"message"`
			ID          string `json:"id"`
			Attachments struct {
				Data []struct {
					Type string `json:"type"`
					URL  string `json:"unshimmed_url"` //nolint:tagliatelle // Nope.
				} `json:"data"`
			} `json:"attachments"`
		} `json:"data"`
	}
)

const (
	StrategyFacebook StrategyType = "facebook"
	StrategyTwitter  StrategyType = "twitter"
)

var (
	ErrInvalidPageContent = errors.New("invalid page content")
	ErrTextNotFound       = errors.New("expected text not found")
	ErrUsernameNotFound   = errors.New("username not found")
	ErrPostNotFound       = errors.New("post not found")
	ErrInvalidURL         = errors.New("invalid URL")
	ErrFetchFailed        = errors.New("cannot fetch post")
	ErrFetchReadFailed    = errors.New("cannot read fetched post")
	ErrScrapeFailed       = errors.New("cannot scrape target")
	ErrInvalidToken       = errors.New("invalid token")
	ErrTweetPrivate       = errors.New("tweet is private or does not exist")
)
