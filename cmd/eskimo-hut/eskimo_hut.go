// SPDX-License-Identifier: ice License 1.0

package main

import (
	"context"
	"strings"

	"github.com/hashicorp/go-multierror"
	"github.com/pkg/errors"

	emaillink "github.com/ice-blockchain/eskimo/auth/email_link"
	"github.com/ice-blockchain/eskimo/cmd/eskimo-hut/api"
	facekyc "github.com/ice-blockchain/eskimo/kyc/face"
	kycquiz "github.com/ice-blockchain/eskimo/kyc/quiz"
	"github.com/ice-blockchain/eskimo/kyc/social"
	"github.com/ice-blockchain/eskimo/users"
	appcfg "github.com/ice-blockchain/wintr/config"
	"github.com/ice-blockchain/wintr/log"
	"github.com/ice-blockchain/wintr/server"
)

// @title						User Accounts, User Devices, User Statistics API
// @version					latest
// @description				API that handles everything related to user's account, user's devices and statistics about those.
// @query.collection.format	multi
// @schemes					https
// @contact.name				ice.io
// @contact.url				https://ice.io
func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	appcfg.MustLoadFromKey(applicationYamlKey, &cfg)
	api.SwaggerInfo.Host = cfg.Host
	api.SwaggerInfo.Version = cfg.Version
	cfg.APIKey = strings.ReplaceAll(cfg.APIKey, "\n", "")
	if cfg.APIKey == "" {
		log.Panic("'api-key' is missing")
	}
	server.New(new(service), applicationYamlKey, swaggerRoot).ListenAndServe(ctx, cancel)
}

func (s *service) RegisterRoutes(router *server.Router) {
	s.registerEskimoRoutes(router)
	s.setupKYCRoutes(router)
	s.setupUserRoutes(router)
	s.setupDevicesRoutes(router)
	s.setupAuthRoutes(router)
}

func (s *service) Init(ctx context.Context, cancel context.CancelFunc) {
	s.usersProcessor = users.StartProcessor(ctx, cancel)
	s.authEmailLinkClient = emaillink.NewClient(ctx, s.usersProcessor, server.Auth(ctx))
	s.socialRepository = social.New(ctx, s.usersProcessor)
	s.quizRepository = kycquiz.NewRepository(ctx, s.usersProcessor)
	s.faceKycClient = facekyc.New(ctx, s.usersProcessor)
}

func (s *service) Close(ctx context.Context) error {
	if ctx.Err() != nil {
		return errors.Wrap(ctx.Err(), "could not close usersProcessor because context ended")
	}

	return multierror.Append( //nolint:wrapcheck // Not needed.
		errors.Wrap(s.quizRepository.Close(), "could not close quiz repository"),
		errors.Wrap(s.socialRepository.Close(), "could not close socialRepository"),
		errors.Wrap(s.authEmailLinkClient.Close(), "could not close authEmailLinkClient"),
		errors.Wrap(s.usersProcessor.Close(), "could not close usersProcessor"),
		errors.Wrap(s.faceKycClient.Close(), "could not close faceKycClient"),
	).ErrorOrNil()
}

func (s *service) CheckHealth(ctx context.Context) error {
	log.Debug("checking health...", "package", "users")

	return errors.Wrapf(s.usersProcessor.CheckHealth(ctx), "processor health check failed")
}
