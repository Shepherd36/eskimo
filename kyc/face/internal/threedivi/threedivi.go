// SPDX-License-Identifier: ice License 1.0

package threedivi

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strings"
	stdlibtime "time"

	"github.com/goccy/go-json"
	"github.com/imroc/req/v3"
	"github.com/pkg/errors"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/prometheus/promql/parser"

	"github.com/ice-blockchain/eskimo/kyc/face/internal"
	"github.com/ice-blockchain/eskimo/users"
	"github.com/ice-blockchain/wintr/log"
	"github.com/ice-blockchain/wintr/time"
)

func init() { //nolint:gochecknoinits // It's the only way to tweak the client.
	req.DefaultClient().SetJsonMarshal(json.Marshal)
	req.DefaultClient().SetJsonUnmarshal(json.Unmarshal)
	req.DefaultClient().GetClient().Timeout = requestDeadline
}

func New3Divi(ctx context.Context, usersRepository internal.UserRepository, cfg *Config) internal.Client {
	if cfg.ThreeDiVi.BAFHost == "" {
		log.Panic(errors.Errorf("no baf-host for 3divi integration"))
	}
	if cfg.ThreeDiVi.BAFToken == "" {
		log.Panic(errors.Errorf("no baf-token for 3divi integration"))
	}
	if cfg.ThreeDiVi.ConcurrentUsers == 0 {
		log.Panic(errors.Errorf("concurrent users is zero for 3divi integration"))
	}
	cfg.ThreeDiVi.BAFHost, _ = strings.CutSuffix(cfg.ThreeDiVi.BAFHost, "/")

	tdv := &threeDivi{
		users: usersRepository,
		cfg:   cfg,
	}
	go tdv.clearUsers(ctx)
	reqCtx, reqCancel := context.WithTimeout(ctx, requestDeadline)
	log.Error(errors.Wrapf(tdv.updateAvailability(reqCtx), "failed to update face availability on startup"))
	reqCancel()
	go tdv.startAvailabilitySyncer(ctx)

	return tdv
}

func (t *threeDivi) startAvailabilitySyncer(ctx context.Context) {
	ticker := stdlibtime.NewTicker(100 * stdlibtime.Millisecond) //nolint:gomnd // .
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			reqCtx, cancel := context.WithTimeout(ctx, requestDeadline)
			log.Error(errors.Wrap(t.updateAvailability(reqCtx), "failed to updateAvailability"))
			cancel()
		case <-ctx.Done():
			return
		}
	}
}

//nolint:funlen // .
func (t *threeDivi) updateAvailability(ctx context.Context) error {
	if t.cfg.ThreeDiVi.AvailabilityURL == "" {
		return nil
	}
	if resp, err := req.
		SetContext(ctx).
		SetRetryCount(10).                                                       //nolint:gomnd // .
		SetRetryBackoffInterval(10*stdlibtime.Millisecond, 1*stdlibtime.Second). //nolint:gomnd // .
		SetRetryHook(func(resp *req.Response, err error) {
			if err != nil {
				log.Error(errors.Wrap(err, "failed to check availability of face auth, retrying... "))
			} else {
				body, bErr := resp.ToString()
				log.Error(errors.Wrapf(bErr, "failed to parse negative response body for check availability of face auth"))
				log.Error(errors.Errorf("failed check availability of face auth with status code:%v, body:%v, retrying... ", resp.GetStatusCode(), body))
			}
		}).
		SetRetryCondition(func(resp *req.Response, err error) bool {
			return err != nil || (resp.GetStatusCode() != http.StatusOK)
		}).
		AddQueryParam("caller", "eskimo-hut").
		Get(t.cfg.ThreeDiVi.AvailabilityURL); err != nil {
		return errors.Wrap(err, "failed to check availability of face auth")
	} else if statusCode := resp.GetStatusCode(); statusCode != http.StatusOK {
		return errors.Errorf("[%v]failed to check availability of face auth", statusCode)
	} else if data, err2 := resp.ToBytes(); err2 != nil {
		return errors.Wrapf(err2, "failed to read body of availability of face auth")
	} else { //nolint:revive // .
		activeUsers, cErr := t.activeUsers(data)
		if cErr != nil {
			return errors.Wrapf(cErr, "failed to parse metrics of availability of face auth")
		}
		t.activeUsersCount.Store(uint64(activeUsers))
	}

	return nil
}

func (t *threeDivi) Available(_ context.Context, userWasPreviouslyForwardedToFaceKYC bool) error {
	return t.isAvailable(userWasPreviouslyForwardedToFaceKYC)
}

//nolint:revive // .
func (t *threeDivi) isAvailable(userWasPreviouslyForwardedToFaceKYC bool) error {
	if int64(t.cfg.ThreeDiVi.ConcurrentUsers)-(int64(t.activeUsersCount.Load())+int64(t.loadBalancedUsersCount.Load())) >= 1 {
		if !userWasPreviouslyForwardedToFaceKYC {
			t.loadBalancedUsersCount.Add(1)
		}

		return nil
	}

	return internal.ErrNotAvailable
}

func (t *threeDivi) clearUsers(ctx context.Context) {
	ticker := stdlibtime.NewTicker(1 * stdlibtime.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			t.loadBalancedUsersCount.Store(0)
		case <-ctx.Done():
			return
		}
	}
}

func (*threeDivi) activeUsers(data []byte) (int, error) {
	p := parser.NewParser(string(data))
	defer p.Close()
	var expparser expfmt.TextParser
	metricFamilies, err := expparser.TextToMetricFamilies(bytes.NewReader(data))
	if err != nil {
		return 0, errors.Wrap(err, "failed to parse metrics for availability of face auth")
	}
	openConns := 0
	if connsMetric, hasConns := metricFamilies[metricOpenConnections]; hasConns {
		for _, metric := range connsMetric.GetMetric() {
			labelMatch := false
			for _, l := range metric.GetLabel() {
				if l.GetValue() == metricOpenConnectionsLabelTCP {
					labelMatch = true
				}
			}
			if labelMatch && metric.GetGauge() != nil {
				openConns = int(metric.GetGauge().GetValue())
			}
		}
	}

	return openConns / connsPerUser, nil
}

func (t *threeDivi) CheckAndUpdateStatus(ctx context.Context, user *users.User) (hasFaceKYCResult bool, err error) {
	bafApplicant, err := t.searchIn3DiviForApplicant(ctx, user.ID)
	if err != nil && !errors.Is(err, errFaceAuthNotStarted) {
		return false, errors.Wrapf(err, "failed to sync face auth status from 3divi BAF")
	}
	usr := t.parseApplicant(user, bafApplicant)
	hasFaceKYCResult = (usr.KYCStepPassed != nil && *usr.KYCStepPassed >= users.LivenessDetectionKYCStep) ||
		(usr.KYCStepBlocked != nil && *usr.KYCStepBlocked > users.NoneKYCStep)
	_, mErr := t.users.ModifyUser(ctx, usr, nil)

	return hasFaceKYCResult, errors.Wrapf(mErr, "failed to update user with face kyc result")
}

//nolint:funlen,revive // .
func (t *threeDivi) Reset(ctx context.Context, user *users.User, fetchState bool) error {
	bafApplicant, err := t.searchIn3DiviForApplicant(ctx, user.ID)
	if err != nil {
		if errors.Is(err, errFaceAuthNotStarted) {
			return nil
		}

		return errors.Wrapf(err, "failed to find matching applicant for userID %v", user.ID)
	}
	var resp *req.Response
	if resp, err = req.
		SetContext(ctx).
		SetRetryCount(10).                                                       //nolint:gomnd // .
		SetRetryBackoffInterval(10*stdlibtime.Millisecond, 1*stdlibtime.Second). //nolint:gomnd // .
		SetRetryHook(func(resp *req.Response, err error) {
			if err != nil {
				log.Error(errors.Wrap(err, "failed to delete face auth state for user, retrying... "))
			} else {
				body, bErr := resp.ToString()
				log.Error(errors.Wrapf(bErr, "failed to parse negative response body for delete face auth state for user"))
				log.Error(errors.Errorf("failed to delete face auth state for user with status code:%v, body:%v, retrying... ", resp.GetStatusCode(), body))
			}
		}).
		SetRetryCondition(func(resp *req.Response, err error) bool {
			return err != nil || (resp.GetStatusCode() != http.StatusOK && resp.GetStatusCode() != http.StatusNoContent)
		}).
		AddQueryParam("caller", "eskimo-hut").
		SetHeader("Authorization", fmt.Sprintf("Bearer %v", t.cfg.ThreeDiVi.BAFToken)).
		SetHeader("X-Secret-Api-Token", t.cfg.ThreeDiVi.SecretAPIToken).
		Delete(fmt.Sprintf("%v/publicapi/api/v2/private/Applicants/%v", t.cfg.ThreeDiVi.BAFHost, bafApplicant.ApplicantID)); err != nil {
		return errors.Wrapf(err, "failed to delete face auth state for userID:%v", user.ID)
	} else if statusCode := resp.GetStatusCode(); statusCode != http.StatusOK && statusCode != http.StatusNoContent {
		return errors.Errorf("[%v]failed to delete face auth state for userID:%v", statusCode, user.ID)
	} else if _, err2 := resp.ToBytes(); err2 != nil {
		return errors.Wrapf(err2, "failed to read body of delete face auth state request for userID:%v", user.ID)
	} else { //nolint:revive // .
		if fetchState {
			_, err = t.CheckAndUpdateStatus(ctx, user)

			return errors.Wrapf(err, "failed to check user's face auth state after reset for userID %v", user.ID)
		}

		return nil
	}
}

//nolint:funlen,gocognit,gocyclo,revive,cyclop //.
func (*threeDivi) parseApplicant(user *users.User, bafApplicant *applicant) *users.User {
	updUser := new(users.User)
	updUser.ID = user.ID
	//nolint:nestif // .
	if bafApplicant != nil && bafApplicant.LastValidationResponse != nil && bafApplicant.Status == statusPassed {
		passedTime := time.New(bafApplicant.LastValidationResponse.CreatedAt)
		if user.KYCStepsCreatedAt != nil && len(*user.KYCStepsCreatedAt) >= int(users.LivenessDetectionKYCStep) {
			updUser.KYCStepsCreatedAt = user.KYCStepsCreatedAt
			updUser.KYCStepsLastUpdatedAt = user.KYCStepsLastUpdatedAt
			if user.KYCStepPassed == nil || *user.KYCStepPassed == 0 {
				stepPassed := users.LivenessDetectionKYCStep
				updUser.KYCStepPassed = &stepPassed
			}
			(*updUser.KYCStepsCreatedAt)[stepIdx(users.FacialRecognitionKYCStep)] = passedTime
			(*updUser.KYCStepsCreatedAt)[stepIdx(users.LivenessDetectionKYCStep)] = passedTime
			(*updUser.KYCStepsLastUpdatedAt)[stepIdx(users.FacialRecognitionKYCStep)] = passedTime
			(*updUser.KYCStepsLastUpdatedAt)[stepIdx(users.LivenessDetectionKYCStep)] = passedTime
		} else {
			times := []*time.Time{passedTime, passedTime}
			updUser.KYCStepsLastUpdatedAt = &times
			stepPassed := users.LivenessDetectionKYCStep
			updUser.KYCStepPassed = &stepPassed
		}
	} else if user.KYCStepsCreatedAt != nil && len(*user.KYCStepsCreatedAt) >= int(users.LivenessDetectionKYCStep) {
		updUser.KYCStepsCreatedAt = user.KYCStepsCreatedAt
		updUser.KYCStepsLastUpdatedAt = user.KYCStepsLastUpdatedAt
		if user.KYCStepPassed != nil && (*user.KYCStepPassed == users.LivenessDetectionKYCStep || *user.KYCStepPassed == users.FacialRecognitionKYCStep) {
			stepPassed := users.NoneKYCStep
			updUser.KYCStepPassed = &stepPassed
		}
		(*updUser.KYCStepsCreatedAt)[stepIdx(users.FacialRecognitionKYCStep)] = nil
		(*updUser.KYCStepsCreatedAt)[stepIdx(users.LivenessDetectionKYCStep)] = nil
		(*updUser.KYCStepsLastUpdatedAt)[stepIdx(users.FacialRecognitionKYCStep)] = nil
		(*updUser.KYCStepsLastUpdatedAt)[stepIdx(users.LivenessDetectionKYCStep)] = nil
	}
	switch {
	case bafApplicant != nil && bafApplicant.LastValidationResponse != nil && bafApplicant.Status == statusFailed && bafApplicant.HasRiskEvents:
		kycStepBlocked := users.FacialRecognitionKYCStep
		updUser.KYCStepBlocked = &kycStepBlocked
	default:
		kycStepBlocked := users.NoneKYCStep
		updUser.KYCStepBlocked = &kycStepBlocked
	}
	user.KYCStepsLastUpdatedAt = updUser.KYCStepsLastUpdatedAt
	user.KYCStepsCreatedAt = updUser.KYCStepsCreatedAt
	if updUser.KYCStepPassed != nil {
		user.KYCStepPassed = updUser.KYCStepPassed
	}
	if updUser.KYCStepBlocked != nil {
		user.KYCStepBlocked = updUser.KYCStepBlocked
	}

	return updUser
}

func stepIdx(step users.KYCStep) int {
	return int(step) - 1
}

func (t *threeDivi) searchIn3DiviForApplicant(ctx context.Context, userID users.UserID) (*applicant, error) {
	if resp, err := req.
		SetContext(ctx).
		SetRetryCount(10).                                                       //nolint:gomnd // .
		SetRetryBackoffInterval(10*stdlibtime.Millisecond, 1*stdlibtime.Second). //nolint:gomnd // .
		SetRetryHook(func(resp *req.Response, err error) {
			if err != nil {
				log.Error(errors.Wrap(err, "failed to match applicantId for user, retrying... "))
			} else {
				body, bErr := resp.ToString()
				log.Error(errors.Wrapf(bErr, "failed to parse negative response body for match applicantId for user"))
				log.Error(errors.Errorf("failed to dmatch applicantId for user with status code:%v, body:%v, retrying... ", resp.GetStatusCode(), body))
			}
		}).
		SetRetryCondition(func(resp *req.Response, err error) bool {
			return err != nil || (resp.GetStatusCode() != http.StatusOK && resp.GetStatusCode() != http.StatusNotFound)
		}).
		SetHeader("Authorization", fmt.Sprintf("Bearer %v", t.cfg.ThreeDiVi.BAFToken)).
		Get(fmt.Sprintf("%v/publicapi/api/v2/private/Applicants/ByReferenceId/%v", t.cfg.ThreeDiVi.BAFHost, userID)); err != nil {
		return nil, errors.Wrapf(err, "failed to match applicantId for userID:%v", userID)
	} else if statusCode := resp.GetStatusCode(); statusCode != http.StatusOK && statusCode != http.StatusNotFound {
		return nil, errors.Errorf("[%v]failed to match applicantIdfor userID:%v", statusCode, userID)
	} else if data, err2 := resp.ToBytes(); err2 != nil {
		return nil, errors.Wrapf(err2, "failed to read body of match applicantId request for userID:%v", userID)
	} else { //nolint:revive // .
		return t.extractApplicant(data)
	}
}

func (*threeDivi) extractApplicant(data []byte) (*applicant, error) {
	var bafApplicant applicant
	if jErr := json.Unmarshal(data, &bafApplicant); jErr != nil {
		return nil, errors.Wrapf(jErr, "failed to decode %v into applicants page", string(data))
	}
	if bafApplicant.Code == codeApplicantNotFound {
		return nil, errFaceAuthNotStarted
	}
	if bafApplicant.LastValidationResponse != nil {
		timeFormat := "2006-01-02T15:04:05.999999"
		var err error
		if bafApplicant.LastValidationResponse.CreatedAt, err = stdlibtime.Parse(timeFormat, bafApplicant.LastValidationResponse.Created); err != nil {
			return nil, errors.Wrapf(err, "failed to parse time at %v", bafApplicant.LastValidationResponse.Created)
		}
	}

	return &bafApplicant, nil
}
