// SPDX-License-Identifier: ice License 1.0

package face

import (
	"context"

	"github.com/ice-blockchain/eskimo/kyc/face/internal"
	"github.com/ice-blockchain/eskimo/kyc/face/internal/threedivi"
	"github.com/ice-blockchain/eskimo/users"
)

type (
	UserRepository = internal.UserRepository
	Config         struct {
		ThreeDiVi threedivi.Config `mapstructure:",squash"` //nolint:tagliatelle // .
	}
	Client interface {
		Reset(ctx context.Context, user *users.User, fetchState bool) error
		CheckStatus(ctx context.Context, user *users.User, nextKYCStep users.KYCStep) (available bool, err error)
	}
)

type (
	client struct {
		client internalClient
	}
	internalClient = internal.Client
)

const (
	applicationYamlKey = "kyc/face"
)
