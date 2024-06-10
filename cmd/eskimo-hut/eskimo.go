// SPDX-License-Identifier: ice License 1.0

package main

import (
	"context"
	"regexp"
	"strings"
	stdlibtime "time"

	"github.com/pkg/errors"

	"github.com/ice-blockchain/eskimo/users"
	"github.com/ice-blockchain/wintr/server"
)

// Public API.

type (
	GetUsersArg struct {
		Keyword string `form:"keyword" required:"true" example:"john"`
		Limit   uint64 `form:"limit" maximum:"1000" example:"10"` // 10 by default.
		Offset  uint64 `form:"offset" example:"5"`
	}
	GetUserByIDArg struct {
		UserID string `uri:"userId" required:"true" example:"did:ethr:0x4B73C58370AEfcEf86A6021afCDe5673511376B2"`
	}
	GetUserByUsernameArg struct {
		Username string `form:"username" required:"true" example:"jdoe"`
	}
	GetTopCountriesArg struct {
		Keyword string `form:"keyword" example:"united states"`
		Limit   uint64 `form:"limit" maximum:"1000" example:"10"` // 10 by default.
		Offset  uint64 `form:"offset" example:"5"`
	}
	GetUserGrowthArg struct {
		TZ   string `form:"tz" example:"+4:30"`
		Days uint64 `form:"days" example:"7"`
	}
	GetReferralAcquisitionHistoryArg struct {
		UserID string `uri:"userId" required:"true" example:"did:ethr:0x4B73C58370AEfcEf86A6021afCDe5673511376B2"`
		Days   uint64 `form:"days" maximum:"30" example:"5"`
	}
	GetReferralsArg struct {
		UserID string `uri:"userId" required:"true" example:"did:ethr:0x4B73C58370AEfcEf86A6021afCDe5673511376B2"`
		Type   string `form:"type" required:"true" example:"T1" enums:"T1,T2,CONTACTS"`
		Limit  uint64 `form:"limit" maximum:"1000" example:"10"` // 10 by default.
		Offset uint64 `form:"offset" example:"5"`
	}
	UserProfile struct {
		*users.UserProfile
		Checksum string `json:"checksum,omitempty" example:"1232412415326543647657"`
	}
)

// Private API.

const (
	everythingNotAllowedInUsernameRegex = `[^.a-zA-Z0-9]+`
)

// Values for server.ErrorResponse#Code.
const (
	invalidKeywordErrorCode = "INVALID_KEYWORD"

	requestingUserIDCtxValueKey = "requestingUserIDCtxValueKey"
)

// .
var (
	everythingNotAllowedInUsernamePattern = regexp.MustCompile(everythingNotAllowedInUsernameRegex)
)

func (s *service) registerEskimoRoutes(router *server.Router) {
	s.setupUserReadRoutes(router)
	s.setupUserReferralRoutes(router)
	s.setupUserStatisticsRoutes(router)
}

func (s *service) setupUserReferralRoutes(router *server.Router) {
	router.
		Group("v1r").
		GET("users/:userId/referral-acquisition-history", server.RootHandler(s.GetReferralAcquisitionHistory)).
		GET("users/:userId/referrals", server.RootHandler(s.GetReferrals))
}

// GetReferralAcquisitionHistory godoc
//
//	@Schemes
//	@Description	Returns the history of referral acquisition for the provided user id.
//	@Tags			Referrals
//	@Accept			json
//	@Produce		json
//	@Param			Authorization		header		string	true	"Insert your access token"		default(Bearer <Add access token here>)
//	@Param			X-Account-Metadata	header		string	false	"Insert your metadata token"	default(<Add metadata token here>)
//	@Param			userId				path		string	true	"ID of the user"
//	@Param			days				query		uint64	false	"Always is 5, cannot be changed due to DB schema"
//	@Success		200					{array}		users.ReferralAcquisition
//	@Failure		400					{object}	server.ErrorResponse	"if validations fail"
//	@Failure		401					{object}	server.ErrorResponse	"if not authorized"
//	@Failure		403					{object}	server.ErrorResponse	"if not allowed"
//	@Failure		422					{object}	server.ErrorResponse	"if syntax fails"
//	@Failure		500					{object}	server.ErrorResponse
//	@Failure		504					{object}	server.ErrorResponse	"if request times out"
//	@Router			/v1r/users/{userId}/referral-acquisition-history [GET].
func (s *service) GetReferralAcquisitionHistory( //nolint:gocritic // False negative.
	ctx context.Context,
	req *server.Request[GetReferralAcquisitionHistoryArg, []*users.ReferralAcquisition],
) (*server.Response[[]*users.ReferralAcquisition], *server.Response[server.ErrorResponse]) {
	res, err := s.usersProcessor.GetReferralAcquisitionHistory(ctx, req.Data.UserID)
	if err != nil {
		return nil, server.Unexpected(errors.Wrapf(err, "error getting referral acquisition history for %#v", req.Data))
	}

	return server.OK(&res), nil
}

// GetReferrals godoc
//
//	@Schemes
//	@Description	Returns the referrals of an user.
//	@Tags			Referrals
//	@Accept			json
//	@Produce		json
//	@Param			Authorization		header		string	true	"Insert your access token"		default(Bearer <Add access token here>)
//	@Param			X-Account-Metadata	header		string	false	"Insert your metadata token"	default(<Add metadata token here>)
//	@Param			userId				path		string	true	"ID of the user"
//	@Param			type				query		string	true	"Type of referrals: `CONTACTS` or `T1` or `T2`"
//	@Param			limit				query		uint64	false	"Limit of elements to return. Defaults to 10"
//	@Param			offset				query		uint64	false	"Number of elements to skip before collecting elements to return"
//	@Success		200					{object}	users.Referrals
//	@Failure		400					{object}	server.ErrorResponse	"if validations fail"
//	@Failure		401					{object}	server.ErrorResponse	"if not authorized"
//	@Failure		403					{object}	server.ErrorResponse	"if not allowed"
//	@Failure		422					{object}	server.ErrorResponse	"if syntax fails"
//	@Failure		500					{object}	server.ErrorResponse
//	@Failure		504					{object}	server.ErrorResponse	"if request times out"
//	@Router			/v1r/users/{userId}/referrals [GET].
func (s *service) GetReferrals( //nolint:gocritic // False negative.
	ctx context.Context,
	req *server.Request[GetReferralsArg, users.Referrals],
) (*server.Response[users.Referrals], *server.Response[server.ErrorResponse]) {
	if req.Data.Limit == 0 {
		req.Data.Limit = 10
	}
	var validType bool
	for _, referralType := range users.ReferralTypes {
		if strings.EqualFold(req.Data.Type, string(referralType)) {
			validType = true

			break
		}
	}
	if !validType {
		err := errors.Errorf("type '%v' is invalid, valid types are %v", req.Data.Type, users.ReferralTypes)

		return nil, server.UnprocessableEntity(err, invalidPropertiesErrorCode)
	}

	referrals, err := s.usersProcessor.GetReferrals(ctx, req.Data.UserID, users.ReferralType(strings.ToUpper(req.Data.Type)), req.Data.Limit, req.Data.Offset)
	if err != nil {
		return nil, server.Unexpected(errors.Wrapf(err, "failed to get referrals for %#v", req.Data))
	}

	return server.OK(referrals), nil
}

func (s *service) setupUserStatisticsRoutes(router *server.Router) {
	router.
		Group("v1r").
		GET("user-statistics/top-countries", server.RootHandler(s.GetTopCountries)).
		GET("user-statistics/user-growth", server.RootHandler(s.GetUserGrowth))
}

// GetTopCountries godoc
//
//	@Schemes
//	@Description	Returns the paginated view of users per country.
//	@Tags			Statistics
//	@Accept			json
//	@Produce		json
//	@Param			Authorization		header		string	true	"Insert your access token"		default(Bearer <Add access token here>)
//	@Param			X-Account-Metadata	header		string	false	"Insert your metadata token"	default(<Add metadata token here>)
//	@Param			keyword				query		string	false	"a keyword to look for in all country codes or names"
//	@Param			limit				query		uint64	false	"Limit of elements to return. Defaults to 10"
//	@Param			offset				query		uint64	false	"Number of elements to skip before collecting elements to return"
//	@Success		200					{array}		users.CountryStatistics
//	@Failure		400					{object}	server.ErrorResponse	"if validations fail"
//	@Failure		401					{object}	server.ErrorResponse	"if not authorized"
//	@Failure		422					{object}	server.ErrorResponse	"if syntax fails"
//	@Failure		500					{object}	server.ErrorResponse
//	@Failure		504					{object}	server.ErrorResponse	"if request times out"
//	@Router			/v1r/user-statistics/top-countries [GET].
func (s *service) GetTopCountries( //nolint:gocritic // False negative.
	ctx context.Context,
	req *server.Request[GetTopCountriesArg, []*users.CountryStatistics],
) (*server.Response[[]*users.CountryStatistics], *server.Response[server.ErrorResponse]) {
	if req.Data.Limit == 0 {
		req.Data.Limit = 10
	}
	result, err := s.usersProcessor.GetTopCountries(ctx, req.Data.Keyword, req.Data.Limit, req.Data.Offset)
	if err != nil {
		return nil, server.Unexpected(errors.Wrapf(err, "failed to get top countries for: %#v", req.Data))
	}

	return server.OK(&result), nil
}

// GetUserGrowth godoc
//
//	@Schemes
//	@Description	Returns statistics about user growth.
//	@Tags			Statistics
//	@Accept			json
//	@Produce		json
//	@Param			Authorization		header		string	true	"Insert your access token"		default(Bearer <Add access token here>)
//	@Param			X-Account-Metadata	header		string	false	"Insert your metadata token"	default(<Add metadata token here>)
//	@Param			days				query		uint64	false	"number of days in the past to look for. Defaults to 3. Max is 90."
//	@Param			tz					query		string	false	"Timezone in format +04:30 or -03:45"
//	@Success		200					{object}	users.UserGrowthStatistics
//	@Failure		400					{object}	server.ErrorResponse	"if validations fail"
//	@Failure		401					{object}	server.ErrorResponse	"if not authorized"
//	@Failure		422					{object}	server.ErrorResponse	"if syntax fails"
//	@Failure		500					{object}	server.ErrorResponse
//	@Failure		504					{object}	server.ErrorResponse	"if request times out"
//	@Router			/v1r/user-statistics/user-growth [GET].
func (s *service) GetUserGrowth( //nolint:gocritic // False negative.
	ctx context.Context,
	req *server.Request[GetUserGrowthArg, users.UserGrowthStatistics],
) (*server.Response[users.UserGrowthStatistics], *server.Response[server.ErrorResponse]) {
	const defaultDays, maxDays = 3, 90
	if req.Data.Days == 0 {
		req.Data.Days = defaultDays
	}
	if req.Data.Days > maxDays {
		req.Data.Days = maxDays
	}
	tz := stdlibtime.UTC
	if req.Data.TZ != "" {
		var invertedTZ string
		if req.Data.TZ[0] == '-' {
			invertedTZ = "+" + req.Data.TZ[1:]
		} else {
			invertedTZ = "-" + req.Data.TZ[1:]
		}
		if t, err := stdlibtime.Parse("-07:00", invertedTZ); err == nil {
			tz = t.Location()
		}
	}
	result, err := s.usersProcessor.GetUserGrowth(ctx, req.Data.Days, tz)
	if err != nil {
		return nil, server.Unexpected(errors.Wrapf(err, "failed to get user growth stats for: %#v", req.Data))
	}

	return server.OK(result), nil
}

func (s *service) setupUserReadRoutes(router *server.Router) {
	router.
		Group("v1r").
		GET("users", server.RootHandler(s.GetUsers)).
		GET("users/:userId", server.RootHandler(s.GetUserByID)).
		GET("user-views/username", server.RootHandler(s.GetUserByUsername))
}

// GetUsers godoc
//
//	@Schemes
//	@Description	Returns a list of user account based on the provided query parameters.
//	@Tags			Accounts
//	@Accept			json
//	@Produce		json
//	@Param			Authorization		header		string	true	"Insert your access token"		default(Bearer <Add access token here>)
//	@Param			X-Account-Metadata	header		string	false	"Insert your metadata token"	default(<Add metadata token here>)
//	@Param			keyword				query		string	true	"A keyword to look for in the usernames"
//	@Param			limit				query		uint64	false	"Limit of elements to return. Defaults to 10"
//	@Param			offset				query		uint64	false	"Elements to skip before starting to look for"
//	@Success		200					{array}		users.MinimalUserProfile
//	@Failure		400					{object}	server.ErrorResponse	"if validations fail"
//	@Failure		401					{object}	server.ErrorResponse	"if not authorized"
//	@Failure		422					{object}	server.ErrorResponse	"if syntax fails"
//	@Failure		500					{object}	server.ErrorResponse
//	@Failure		504					{object}	server.ErrorResponse	"if request times out"
//	@Router			/v1r/users [GET].
func (s *service) GetUsers( //nolint:gocritic // False negative.
	ctx context.Context,
	req *server.Request[GetUsersArg, []*users.MinimalUserProfile],
) (*server.Response[[]*users.MinimalUserProfile], *server.Response[server.ErrorResponse]) {
	key := string(everythingNotAllowedInUsernamePattern.ReplaceAll([]byte(strings.ToLower(req.Data.Keyword)), []byte("")))
	if key == "" || !strings.EqualFold(key, req.Data.Keyword) {
		err := errors.Errorf("username: %v is invalid, it should match regex: %v", req.Data.Keyword, everythingNotAllowedInUsernamePattern)

		return nil, server.BadRequest(err, invalidKeywordErrorCode)
	}
	if req.Data.Limit == 0 {
		req.Data.Limit = 10
	}
	resp, err := s.usersProcessor.GetUsers(ctx, req.Data.Keyword, req.Data.Limit, req.Data.Offset)
	if err != nil {
		return nil, server.Unexpected(errors.Wrapf(err, "failed to get users by %#v", req.Data))
	}

	return server.OK(&resp), nil
}

// GetUserByID godoc
//
//	@Schemes
//	@Description	Returns an user's account.
//	@Tags			Accounts
//	@Accept			json
//	@Produce		json
//	@Param			Authorization		header		string	true	"Insert your access token"		default(Bearer <Add access token here>)
//	@Param			X-Account-Metadata	header		string	false	"Insert your metadata token"	default(<Add metadata token here>)
//	@Param			userId				path		string	true	"ID of the user"
//	@Success		200					{object}	UserProfile
//	@Failure		400					{object}	server.ErrorResponse	"if validations fail"
//	@Failure		401					{object}	server.ErrorResponse	"if not authorized"
//	@Failure		404					{object}	server.ErrorResponse	"if not found"
//	@Failure		422					{object}	server.ErrorResponse	"if syntax fails"
//	@Failure		500					{object}	server.ErrorResponse
//	@Failure		504					{object}	server.ErrorResponse	"if request times out"
//	@Router			/v1r/users/{userId} [GET].
func (s *service) GetUserByID( //nolint:gocritic // False negative.
	ctx context.Context,
	req *server.Request[GetUserByIDArg, UserProfile],
) (*server.Response[UserProfile], *server.Response[server.ErrorResponse]) {
	if req.AuthenticatedUser.Role == adminRole && req.Data.UserID != req.AuthenticatedUser.UserID {
		ctx = context.WithValue(ctx, requestingUserIDCtxValueKey, req.Data.UserID) //nolint:revive,staticcheck //.
	}
	usr, err := s.usersProcessor.GetUserByID(ctx, req.Data.UserID)
	if err != nil {
		if errors.Is(err, users.ErrNotFound) {
			return nil, server.NotFound(errors.Wrapf(err, "user with id `%v` was not found", req.Data.UserID), userNotFoundErrorCode)
		}

		return nil, server.Unexpected(errors.Wrapf(err, "failed to get user by id: %v", req.Data.UserID))
	}

	return server.OK(&UserProfile{UserProfile: usr, Checksum: usr.Checksum()}), nil
}

// GetUserByUsername godoc
//
//	@Schemes
//	@Description	Returns public information about an user account based on an username, making sure the username is valid first.
//	@Tags			Accounts
//	@Accept			json
//	@Produce		json
//	@Param			Authorization		header		string	true	"Insert your access token"		default(Bearer <Add access token here>)
//	@Param			X-Account-Metadata	header		string	false	"Insert your metadata token"	default(<Add metadata token here>)
//	@Param			username			query		string	true	"username of the user. It will validate it first"
//	@Success		200					{object}	UserProfile
//	@Failure		400					{object}	server.ErrorResponse	"if validations fail"
//	@Failure		401					{object}	server.ErrorResponse	"if not authorized"
//	@Failure		404					{object}	server.ErrorResponse	"if not found"
//	@Failure		422					{object}	server.ErrorResponse	"if syntax fails"
//	@Failure		500					{object}	server.ErrorResponse
//	@Failure		504					{object}	server.ErrorResponse	"if request times out"
//	@Router			/v1r/user-views/username [GET].
func (s *service) GetUserByUsername( //nolint:gocritic // False negative.
	ctx context.Context,
	req *server.Request[GetUserByUsernameArg, UserProfile],
) (*server.Response[UserProfile], *server.Response[server.ErrorResponse]) {
	if !users.CompiledUsernameRegex.MatchString(req.Data.Username) {
		err := errors.Errorf("username: %v is invalid, it should match regex: %v", req.Data.Username, users.UsernameRegex)

		return nil, server.BadRequest(err, invalidUsernameErrorCode)
	}

	resp, err := s.usersProcessor.GetUserByUsername(ctx, strings.ToLower(req.Data.Username))
	if err != nil {
		if errors.Is(err, users.ErrNotFound) {
			return nil, server.NotFound(errors.Wrapf(err, "user with username `%v` was not found", req.Data.Username), userNotFoundErrorCode)
		}

		return nil, server.Unexpected(errors.Wrapf(err, "failed to get user by username: %v", req.Data.Username))
	}

	return server.OK(&UserProfile{UserProfile: resp}), nil
}
