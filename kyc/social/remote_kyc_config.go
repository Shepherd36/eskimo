// SPDX-License-Identifier: ice License 1.0

package social

import (
	"context"
	"net/http"
	"strings"
	"sync/atomic"
	"text/template"
	stdlibtime "time"

	"github.com/goccy/go-json"
	"github.com/imroc/req/v3"
	"github.com/pkg/errors"

	"github.com/ice-blockchain/wintr/log"
)

func init() { //nolint:gochecknoinits // It's the only way to tweak the client.
	req.DefaultClient().SetJsonMarshal(json.Marshal)
	req.DefaultClient().SetJsonUnmarshal(json.Unmarshal)
	req.DefaultClient().GetClient().Timeout = requestDeadline
}

func (r *repository) startKYCConfigJSONSyncer(ctx context.Context) {
	ticker := stdlibtime.NewTicker(stdlibtime.Minute)
	defer ticker.Stop()
	r.cfg.kycConfigJSON1 = new(atomic.Pointer[kycConfigJSON1])
	r.cfg.kycConfigJSON2 = new(atomic.Pointer[kycConfigJSON2])
	log.Panic(errors.Wrap(r.syncKYCConfigJSON1(ctx), "failed to syncKYCConfigJSON1")) //nolint:revive // .
	log.Panic(errors.Wrap(r.syncKYCConfigJSON2(ctx), "failed to syncKYCConfigJSON2"))

	for {
		select {
		case <-ticker.C:
			reqCtx, cancel := context.WithTimeout(ctx, requestDeadline)
			log.Error(errors.Wrap(r.syncKYCConfigJSON1(reqCtx), "failed to syncKYCConfigJSON1"))
			cancel()
			reqCtx, cancel = context.WithTimeout(ctx, requestDeadline)
			log.Error(errors.Wrap(r.syncKYCConfigJSON2(reqCtx), "failed to syncKYCConfigJSON2"))
			cancel()
		case <-ctx.Done():
			return
		}
	}
}

//nolint:funlen,gomnd,nestif,dupl,revive // .
func (r *repository) syncKYCConfigJSON1(ctx context.Context) error {
	if resp, err := req.
		SetContext(ctx).
		SetRetryCount(25).
		SetRetryBackoffInterval(10*stdlibtime.Millisecond, 1*stdlibtime.Second).
		SetRetryHook(func(resp *req.Response, err error) {
			if err != nil {
				log.Error(errors.Wrap(err, "failed to fetch KYCConfigJSON, retrying...")) //nolint:revive // .
			} else {
				log.Error(errors.Errorf("failed to fetch KYCConfigJSON with status code:%v, retrying...", resp.GetStatusCode())) //nolint:revive // .
			}
		}).
		SetRetryCondition(func(resp *req.Response, err error) bool {
			return err != nil || resp.GetStatusCode() != http.StatusOK
		}).
		SetHeader("Accept", "application/json").
		SetHeader("Cache-Control", "no-cache, no-store, must-revalidate").
		SetHeader("Pragma", "no-cache").
		SetHeader("Expires", "0").
		Get(r.cfg.ConfigJSONURL1); err != nil {
		return errors.Wrapf(err, "failed to get fetch `%v`", r.cfg.ConfigJSONURL1)
	} else if data, err2 := resp.ToBytes(); err2 != nil {
		return errors.Wrapf(err2, "failed to read body of `%v`", r.cfg.ConfigJSONURL1)
	} else { //nolint:revive // .
		var kycConfig kycConfigJSON1
		if err = json.UnmarshalContext(ctx, data, &kycConfig); err != nil {
			return errors.Wrapf(err, "failed to unmarshal into %#v, data: %v", kycConfig, string(data))
		}
		if body := string(data); !strings.Contains(body, "social1-kyc") {
			return errors.Errorf("there's something wrong with the KYCConfigJSON body: %v", body)
		}
		if pattern := kycConfig.Social1KYC.XPostPattern; pattern != "" {
			if kycConfig.Social1KYC.xPostPatternTemplate, err = template.New("kycCfg.Social1KYC.XPostPattern").Parse(pattern); err != nil {
				return errors.Wrapf(err, "failed to parse kycCfg.Social1KYC.xPostPatternTemplate `%v`", pattern)
			}
		}
		r.cfg.kycConfigJSON1.Swap(&kycConfig)

		return nil
	}
}

//nolint:funlen,gomnd,nestif,dupl,revive // .
func (r *repository) syncKYCConfigJSON2(ctx context.Context) error {
	if resp, err := req.
		SetContext(ctx).
		SetRetryCount(25).
		SetRetryBackoffInterval(10*stdlibtime.Millisecond, 1*stdlibtime.Second).
		SetRetryHook(func(resp *req.Response, err error) {
			if err != nil {
				log.Error(errors.Wrap(err, "failed to fetch KYCConfigJSON, retrying...")) //nolint:revive // .
			} else {
				log.Error(errors.Errorf("failed to fetch KYCConfigJSON with status code:%v, retrying...", resp.GetStatusCode())) //nolint:revive // .
			}
		}).
		SetRetryCondition(func(resp *req.Response, err error) bool {
			return err != nil || resp.GetStatusCode() != http.StatusOK
		}).
		SetHeader("Accept", "application/json").
		SetHeader("Cache-Control", "no-cache, no-store, must-revalidate").
		SetHeader("Pragma", "no-cache").
		SetHeader("Expires", "0").
		Get(r.cfg.ConfigJSONURL2); err != nil {
		return errors.Wrapf(err, "failed to get fetch `%v`", r.cfg.ConfigJSONURL2)
	} else if data, err2 := resp.ToBytes(); err2 != nil {
		return errors.Wrapf(err2, "failed to read body of `%v`", r.cfg.ConfigJSONURL2)
	} else { //nolint:revive // .
		var kycConfig kycConfigJSON2
		if err = json.UnmarshalContext(ctx, data, &kycConfig); err != nil {
			return errors.Wrapf(err, "failed to unmarshal into %#v, data: %v", kycConfig, string(data))
		}
		if body := string(data); !strings.Contains(body, "social2-kyc") {
			return errors.Errorf("there's something wrong with the KYCConfigJSON body: %v", body)
		}
		if pattern := kycConfig.Social2KYC.XPostPattern; pattern != "" {
			if kycConfig.Social2KYC.xPostPatternTemplate, err = template.New("kycCfg.Social2KYC.XPostPattern").Parse(pattern); err != nil {
				return errors.Wrapf(err, "failed to parse kycCfg.Social2KYC.xPostPatternTemplate `%v`", pattern)
			}
		}
		r.cfg.kycConfigJSON2.Swap(&kycConfig)

		return nil
	}
}
