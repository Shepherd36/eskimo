// SPDX-License-Identifier: ice License 1.0

package threedivi

import (
	"sync/atomic"
	stdlibtime "time"

	"github.com/pkg/errors"

	"github.com/ice-blockchain/eskimo/kyc/face/internal"
)

type (
	threeDivi struct {
		users                  internal.UserRepository
		cfg                    *Config
		loadBalancedUsersCount atomic.Uint64
		activeUsersCount       atomic.Uint64
	}
	Config struct {
		ThreeDiVi struct {
			BAFHost         string `yaml:"bafHost"`
			BAFToken        string `yaml:"bafToken"`
			SecretAPIToken  string `yaml:"secretApiToken"`
			AvailabilityURL string `yaml:"availabilityUrl"`
			ConcurrentUsers int    `yaml:"concurrentUsers"`
		} `yaml:"threeDiVi"`
	}
)

// Private API.
type (
	applicant struct {
		Code                   string              `json:"code"`
		LastValidationResponse *validationResponse `json:"lastValidationResponse"`
		ApplicantID            string              `json:"applicantId"`
		Status                 int                 `json:"status"`
		HasRiskEvents          bool                `json:"hasRiskEvents"`
	}
	validationResponse struct {
		CreatedAt            stdlibtime.Time
		Created              string `json:"created"`
		ResponseStatusName   string `json:"responseStatusName"`
		ValidationResponseID int    `json:"validationResponseId"`
		ResponseStatus       int    `json:"responseStatus"`
	}
)

const (
	requestDeadline               = 30 * stdlibtime.Second
	metricOpenConnections         = "stunner_listener_connections"
	connsPerUser                  = 2
	metricOpenConnectionsLabelTCP = "default/tcp-gateway/tcp-listener"
	statusPassed                  = 1
	statusFailed                  = 2
	codeApplicantNotFound         = "120024"
)

var ( //nolint:gofumpt // .
	errFaceAuthNotStarted = errors.New("face auth not started")
)
