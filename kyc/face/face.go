// SPDX-License-Identifier: ice License 1.0

package face

import (
	"context"
	stdlibtime "time"

	"github.com/pkg/errors"

	"github.com/ice-blockchain/eskimo/kyc/face/internal"
	"github.com/ice-blockchain/eskimo/kyc/face/internal/threedivi"
	"github.com/ice-blockchain/eskimo/users"
	appcfg "github.com/ice-blockchain/wintr/config"
	"github.com/ice-blockchain/wintr/connectors/storage/v2"
	"github.com/ice-blockchain/wintr/log"
	"github.com/ice-blockchain/wintr/time"
)

func New(ctx context.Context, usersRep UserRepository) Client {
	var cfg Config
	appcfg.MustLoadFromKey(applicationYamlKey, &cfg)
	if cfg.UnexpectedErrorsAllowed == 0 {
		cfg.UnexpectedErrorsAllowed = 5
	}
	db := storage.MustConnect(ctx, ddl, applicationYamlKey)
	cl := &client{client: threedivi.New3Divi(ctx, usersRep, &cfg.ThreeDiVi), cfg: cfg, db: db}
	go cl.clearErrs(ctx)

	return cl
}

//nolint:funlen,gocognit,revive,gocyclo,cyclop // .
func (c *client) CheckStatus(ctx context.Context, user *users.User, nextKYCStep users.KYCStep) (bool, error) {
	kycFaceAvailable := false
	if errs := c.unexpectedErrors.Load(); errs >= c.cfg.UnexpectedErrorsAllowed {
		log.Error(errors.Errorf("some unexpected error occurred recently for user id %v", user.ID))

		return false, nil
	}
	userWasPreviouslyForwardedToFaceKYC, err := c.checkIfUserWasForwardedToFaceKYC(ctx, user.ID)
	if err != nil {
		return false, errors.Wrapf(err, "failed to check if user id %v was forwarded to face kyc before", user.ID)
	}
	hasResult := false
	now := time.Now()
	if userWasPreviouslyForwardedToFaceKYC && user.LastMiningStartedAt.Before(*now.Time) {
		if hasResult, err = c.client.CheckAndUpdateStatus(ctx, user); err != nil {
			c.unexpectedErrors.Add(1)
			log.Error(errors.Wrapf(err, "[unexpected]failed to update face auth status for user ID %s", user.ID))

			return false, nil
		}
	}
	if hasResult || user.LastMiningStartedAt.After(*now.Time) { // User canceled and started mining when there were no slots.
		if dErr := c.deleteUserForwarded(ctx, user.ID); dErr != nil {
			return false, errors.Wrapf(err, "failed to delete user forwarded to face kyc for user id %v", user.ID)
		}
	}
	if !hasResult || nextKYCStep == users.LivenessDetectionKYCStep {
		availabilityErr := c.client.Available(ctx, userWasPreviouslyForwardedToFaceKYC)
		if availabilityErr == nil {
			kycFaceAvailable = true
			if fErr := c.saveUserForwarded(ctx, user.ID, now); fErr != nil {
				return false, errors.Wrapf(fErr, "failed ")
			}
		} else if !errors.Is(availabilityErr, internal.ErrNotAvailable) {
			c.unexpectedErrors.Add(1)
			log.Error(errors.Wrapf(availabilityErr, "[unexpected]face auth is unavailable for userID %v KYCStep %v", user.ID, nextKYCStep))
		}
	}
	if !kycFaceAvailable && userWasPreviouslyForwardedToFaceKYC {
		// User will bypass face (no slots) and mine, will recreate forward next time not to sync state next time.
		if dErr := c.deleteUserForwarded(ctx, user.ID); dErr != nil {
			return false, errors.Wrapf(err, "failed to delete user forwarded to face kyc for user id %v", user.ID)
		}
	}

	return kycFaceAvailable, nil
}

func (c *client) deleteUserForwarded(ctx context.Context, userID string) error {
	_, err := storage.Exec(ctx, c.db, "DELETE FROM users_forwarded_to_face_kyc WHERE user_id = $1;", userID)

	return errors.Wrapf(err, "failed to delete user forwarded to face kyc for userID %v", userID)
}

func (c *client) saveUserForwarded(ctx context.Context, userID string, now *time.Time) error {
	_, err := storage.Exec(ctx, c.db, "INSERT INTO users_forwarded_to_face_kyc(forwarded_at, user_id) values ($1, $2) ON CONFLICT DO NOTHING;", now.Time, userID)

	return errors.Wrapf(err, "failed to save user forwarded to face kyc for userID %v", userID)
}

func (c *client) checkIfUserWasForwardedToFaceKYC(ctx context.Context, userID string) (bool, error) {
	_, err := storage.Get[struct {
		ForwardedAt *time.Time `db:"forwarded_at"`
		UserID      string     `db:"user_id"`
	}](ctx, c.db, "SELECT * FROM users_forwarded_to_face_kyc WHERE user_id = $1;", userID)
	if err != nil {
		if storage.IsErr(err, storage.ErrNotFound) {
			return false, nil
		}

		return false, errors.Wrapf(err, "failed to check if user was forwarded to face kyc")
	}

	return true, nil
}

func (c *client) clearErrs(ctx context.Context) {
	ticker := stdlibtime.NewTicker(refreshTime)
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

func (c *client) Close() error {
	return c.db.Close() //nolint:wrapcheck // .
}
