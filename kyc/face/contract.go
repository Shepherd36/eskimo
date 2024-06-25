// SPDX-License-Identifier: ice License 1.0

package face

import (
	"context"
	"sync/atomic"
	stdlibtime "time"

	"github.com/ice-blockchain/eskimo/kyc/face/internal"
	"github.com/ice-blockchain/eskimo/kyc/face/internal/threedivi"
	"github.com/ice-blockchain/eskimo/users"
)

type (
	UserRepository = internal.UserRepository
	Config         struct {
		ThreeDiVi               threedivi.Config `mapstructure:",squash"` //nolint:tagliatelle // .
		UnexpectedErrorsAllowed uint64           `yaml:"unexpectedErrorsAllowed" mapstructure:"unexpectedErrorsAllowed"`
	}
	Client interface {
		Reset(ctx context.Context, user *users.User, fetchState bool) error
		CheckStatus(ctx context.Context, user *users.User, nextKYCStep users.KYCStep) (available bool, err error)
	}
)

type (
	client struct {
		client           internalClient
		cfg              Config
		unexpectedErrors atomic.Uint64
	}
	internalClient = internal.Client
)

const (
	applicationYamlKey = "kyc/face"
	refreshTime        = 1 * stdlibtime.Minute
)
