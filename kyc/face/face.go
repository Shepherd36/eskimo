// SPDX-License-Identifier: ice License 1.0

package face

import (
	"context"

	"github.com/pkg/errors"

	"github.com/ice-blockchain/eskimo/kyc/face/internal/threedivi"
	"github.com/ice-blockchain/eskimo/users"
	appcfg "github.com/ice-blockchain/wintr/config"
	"github.com/ice-blockchain/wintr/log"
)

func New(usersRep UserRepository) Client {
	var cfg Config
	appcfg.MustLoadFromKey(applicationYamlKey, &cfg)

	return &client{client: threedivi.New3Divi(usersRep, &cfg.ThreeDiVi)}
}

func (c *client) CheckStatus(ctx context.Context, user *users.User, nextKYCStep users.KYCStep) (bool, error) {
	kycFaceAvailable := false
	if hasResult, err := c.client.CheckAndUpdateStatus(ctx, user); err != nil {
		return false, errors.Wrapf(err, "failed to update face auth status for user ID %s", user.ID)
	} else if !hasResult || nextKYCStep == users.LivenessDetectionKYCStep {
		availabilityErr := c.client.Available(ctx)
		if availabilityErr == nil {
			kycFaceAvailable = true
		} else {
			log.Error(errors.Wrapf(err, "face auth is unavailable for userID %v KYCStep %v", user.ID, nextKYCStep))
		}
	}

	return kycFaceAvailable, nil
}

func (c *client) Reset(ctx context.Context, user *users.User, fetchState bool) error {
	return errors.Wrapf(c.client.Reset(ctx, user, fetchState), "failed to reset face auth state for userID %s", user.ID)
}
