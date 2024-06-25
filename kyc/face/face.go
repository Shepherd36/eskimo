// SPDX-License-Identifier: ice License 1.0

package face

import (
	"context"
	"time"

	"github.com/pkg/errors"

	"github.com/ice-blockchain/eskimo/kyc/face/internal"
	"github.com/ice-blockchain/eskimo/kyc/face/internal/threedivi"
	"github.com/ice-blockchain/eskimo/users"
	appcfg "github.com/ice-blockchain/wintr/config"
	"github.com/ice-blockchain/wintr/log"
)

func New(ctx context.Context, usersRep UserRepository) Client {
	var cfg Config
	appcfg.MustLoadFromKey(applicationYamlKey, &cfg)
	if cfg.UnexpectedErrorsAllowed == 0 {
		cfg.UnexpectedErrorsAllowed = 5
	}
	cl := &client{client: threedivi.New3Divi(usersRep, &cfg.ThreeDiVi), cfg: cfg}
	go cl.clearErrs(ctx)

	return cl
}

func (c *client) CheckStatus(ctx context.Context, user *users.User, nextKYCStep users.KYCStep) (bool, error) {
	kycFaceAvailable := false
	if errs := c.unexpectedErrors.Load(); errs >= c.cfg.UnexpectedErrorsAllowed {
		log.Error(errors.Errorf("some unexpected error occurred recently for user id %v", user.ID))

		return false, nil
	}
	availabilityErr := c.client.Available(ctx)
	if availabilityErr == nil {
		kycFaceAvailable = true
		if _, err := c.client.CheckAndUpdateStatus(ctx, user); err != nil {
			c.unexpectedErrors.Add(1)
			log.Error(errors.Wrapf(err, "[unexpected]failed to update face auth status for user ID %s", user.ID))

			return false, nil
		}

		return kycFaceAvailable, nil
	}
	if !errors.Is(availabilityErr, internal.ErrNotAvailable) {
		c.unexpectedErrors.Add(1)
		log.Error(errors.Wrapf(availabilityErr, "[unexpected]face auth is unavailable for userID %v KYCStep %v", user.ID, nextKYCStep))
	}

	return kycFaceAvailable, nil
}

func (c *client) clearErrs(ctx context.Context) {
	ticker := time.NewTicker(refreshTime)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			c.unexpectedErrors.Store(0)
		case <-ctx.Done():
			return
		}
	}
}

func (c *client) Reset(ctx context.Context, user *users.User, fetchState bool) error {
	return errors.Wrapf(c.client.Reset(ctx, user, fetchState), "failed to reset face auth state for userID %s", user.ID)
}
