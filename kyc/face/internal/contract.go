// SPDX-License-Identifier: ice License 1.0

package internal

import (
	"context"
	"mime/multipart"

	"github.com/ice-blockchain/eskimo/users"
)

type (
	Client interface {
		Available(ctx context.Context) error
		CheckAndUpdateStatus(ctx context.Context, userID string) (hasFaceKYCResult bool, err error)
		Reset(ctx context.Context, userID string) error
	}
	UserRepository interface {
		ModifyUser(ctx context.Context, usr *users.User, profilePicture *multipart.FileHeader) error
	}
)
