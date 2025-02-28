// SPDX-License-Identifier: ice License 1.0

package users

import (
	"context"
	"fmt"
	stdlibtime "time"

	"github.com/goccy/go-json"
	"github.com/pkg/errors"

	messagebroker "github.com/ice-blockchain/wintr/connectors/message_broker"
	storage "github.com/ice-blockchain/wintr/connectors/storage/v2"
	"github.com/ice-blockchain/wintr/time"
)

func (s *miningSessionSource) Process(ctx context.Context, msg *messagebroker.Message) error {
	if ctx.Err() != nil || len(msg.Value) == 0 {
		return errors.Wrap(ctx.Err(), "unexpected deadline while processing message")
	}
	ses := new(miningSession)
	if err := json.UnmarshalContext(ctx, msg.Value, ses); err != nil || ses.UserID == "" || ses.StartedAt.IsNil() {
		return errors.Wrapf(err, "process: cannot unmarshall %v into %#v", string(msg.Value), ses)
	}
	usr, err := s.updateMiningSession(ctx, ses)
	if err != nil {
		return errors.Wrapf(err, "failed to updateMiningSession for %#v", ses)
	}

	return errors.Wrapf(s.incrementTotalActiveUsersCount(ctx, ses), "failed to incrementTotalActiveUsersCount for: %v", usr.ID)
}

func (u *User) IsFirstMiningAfterHumanVerification(minMiningSessionDuration stdlibtime.Duration) bool {
	if !u.IsHuman() {
		return false
	}

	return (*u.KYCStepsCreatedAt)[LivenessDetectionKYCStep-1].Equal(*(*u.KYCStepsLastUpdatedAt)[LivenessDetectionKYCStep-1].Time) &&
		(*u.KYCStepsCreatedAt)[LivenessDetectionKYCStep-1].Before(*u.LastMiningStartedAt.Time) &&
		(*u.KYCStepsCreatedAt)[LivenessDetectionKYCStep-1].Add(minMiningSessionDuration).After(*u.LastMiningStartedAt.Time)
}

func (u *User) HadAtLeastAMiningAfterHumanVerification() bool {
	if !u.IsHuman() {
		return false
	}

	return (*u.KYCStepsCreatedAt)[LivenessDetectionKYCStep-1].Before(*u.LastMiningStartedAt.Time)
}

//nolint:revive // Intended.
func (u *User) isFirstMiningAfterHumanVerification(repo *repository) bool {
	return u.IsFirstMiningAfterHumanVerification(repo.cfg.GlobalAggregationInterval.MinMiningSessionDuration)
}

//nolint:revive // Intended.
func (u *User) hadAtLeastAMiningAfterHumanVerification(_ *repository) bool {
	return u.HadAtLeastAMiningAfterHumanVerification()
}

//nolint:revive // Nope.
func (u *User) IsHuman() bool {
	return u != nil && u.KYCStepPassed != nil && u.KYCStepsCreatedAt != nil && u.KYCStepsLastUpdatedAt != nil && !u.LastMiningStartedAt.IsNil() &&
		*u.KYCStepPassed >= LivenessDetectionKYCStep &&
		len(*u.KYCStepsCreatedAt) >= int(LivenessDetectionKYCStep) &&
		len(*u.KYCStepsLastUpdatedAt) >= int(LivenessDetectionKYCStep) &&
		!(*u.KYCStepsCreatedAt)[LivenessDetectionKYCStep-1].IsNil() &&
		!(*u.KYCStepsLastUpdatedAt)[LivenessDetectionKYCStep-1].IsNil()
}

func (s *miningSessionSource) updateMiningSession(ctx context.Context, ses *miningSession) (*User, error) {
	sql := fmt.Sprintf(`
		UPDATE users
		SET updated_at = $1,
			last_mining_started_at = $2,
			last_mining_ended_at = $3,
			mining_boost_level = $5
		WHERE id = $4
		  AND (last_mining_started_at IS NULL OR (extract(epoch from last_mining_started_at)::bigint/%[1]v) != (extract(epoch from $2::timestamp)::bigint/%[1]v))
		  AND (last_mining_ended_at IS NULL OR (extract(epoch from last_mining_ended_at)::bigint/%[1]v) != (extract(epoch from $3::timestamp)::bigint/%[1]v))
	    RETURNING *`,
		uint64(s.cfg.GlobalAggregationInterval.MinMiningSessionDuration/stdlibtime.Second))
	usr, err := storage.ExecOne[User](ctx, s.db, sql,
		time.Now().Time,
		ses.LastNaturalMiningStartedAt.Time,
		ses.EndedAt.Time,
		ses.UserID,
		ses.MiningBoostLevel,
	)
	if err != nil && storage.IsErr(err, storage.ErrNotFound) {
		err = ErrDuplicate
	}

	return usr, errors.Wrapf(err,
		"failed to update users.last_mining_started_at to %v, users.last_mining_ended_at to %v, for userID: %v", ses.LastNaturalMiningStartedAt.Time, ses.EndedAt, ses.UserID) //nolint:lll // .
}
