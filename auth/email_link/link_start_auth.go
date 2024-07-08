// SPDX-License-Identifier: ice License 1.0

package emaillinkiceauth

import (
	"context"
	"crypto/rand"
	"fmt"
	"math/big"
	"sync/atomic"
	stdlibtime "time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/hashicorp/go-multierror"
	"github.com/pkg/errors"

	"github.com/ice-blockchain/eskimo/users"
	"github.com/ice-blockchain/wintr/connectors/storage/v2"
	"github.com/ice-blockchain/wintr/email"
	"github.com/ice-blockchain/wintr/log"
	"github.com/ice-blockchain/wintr/terror"
	"github.com/ice-blockchain/wintr/time"
)

//nolint:funlen,gocognit,revive,gocritic,lll //.
func (c *client) SendSignInLinkToEmail(ctx context.Context, emailValue, deviceUniqueID, language, clientIP string) (posInQueue int64, rateLimit, loginSession string, err error) {
	if ctx.Err() != nil {
		return 0, "", "", errors.Wrap(ctx.Err(), "send sign in link to email failed because context failed")
	}
	now := time.Now()
	id := loginID{emailValue, deviceUniqueID}
	loginSessionNumber := now.Time.Unix() / int64(sameIPCheckRate.Seconds())
	oldEmail := users.ConfirmedEmail(ctx)
	if oldEmail == "" {
		posInQueue, rateLimit, err = c.enqueueLoginAttempt(ctx, now, emailValue)
		if err != nil {
			if errors.Is(err, errAlreadyEnqueued) {
				loginSession, err = c.getExistingLoginSession(ctx, &id, loginSessionNumber, clientIP)

				return posInQueue, rateLimit, loginSession, errors.Wrapf(err, "failed to fetch existing login session for email %v", id.Email)
			}

			return 0, "", "", errors.Wrapf(err, "failed to enqueue email %v", emailValue)
		}
	}

	if vErr := c.validateEmailSignIn(ctx, &id); vErr != nil {
		return 0, "", "", errors.Wrapf(vErr, "can't validate email sign in for:%#v", id)
	}
	if oldEmail != "" {
		loginSessionNumber = 0
		clientIP = "" //nolint:revive // .
		oldID := loginID{oldEmail, deviceUniqueID}
		if vErr := c.validateEmailModification(ctx, emailValue, &oldID); vErr != nil {
			return 0, "", "", errors.Wrapf(vErr, "can't validate modification email for:%#v", oldID)
		}
	}
	confirmationCode := generateConfirmationCode()
	loginSession, err = c.generateLoginSession(&id, clientIP, oldEmail, loginSessionNumber)
	if err != nil {
		return 0, "", "", errors.Wrap(err, "can't call generateLoginSession")
	}
	if uErr := c.upsertEmailLinkSignIn(ctx, id.Email, id.DeviceUniqueID, confirmationCode, language, now); uErr != nil {
		if errors.Is(uErr, ErrUserDuplicate) {
			oldLoginSession, oErr := c.restoreOldLoginSession(&id, clientIP, oldEmail, loginSessionNumber)
			if oErr != nil {
				return 0, "", "", multierror.Append( //nolint:wrapcheck // .
					errors.Wrapf(oErr, "failed to calculate oldLoginSession"),
					errors.Wrapf(uErr, "failed to store/update email link sign ins for id:%#v", id),
				).ErrorOrNil()
			}

			return posInQueue, rateLimit, oldLoginSession, nil
		}

		return 0, "", "", errors.Wrapf(uErr, "failed to store/update email link sign ins for id:%#v", id)
	}
	if oldEmail != "" {
		if sendModEmailErr := c.sendEmailWithType(ctx, modifyEmailType, language, []string{id.Email}, []string{confirmationCode}); sendModEmailErr != nil {
			return 0, "", loginSession, errors.Wrapf(sendModEmailErr, "failed to send validation email for id:%#v", id)
		}
	}

	return posInQueue, rateLimit, loginSession, nil
}

func (c *client) getExistingLoginSession(ctx context.Context, id *loginID, loginSessionNumber int64, clientIP string) (loginSession string, err error) {
	_, sErr := c.getEmailLinkSignInByPk(ctx, id, "")
	if sErr != nil {
		return "", errors.Wrapf(sErr, "failed to get user info by email:%v", id.Email)
	}
	loginSession, err = c.generateLoginSession(id, clientIP, "", loginSessionNumber)
	if err != nil {
		return "", errors.Wrap(err, "can't call generateLoginSession")
	}

	return loginSession, nil
}

func (c *client) restoreOldLoginSession(id *loginID, clientIP, oldEmail string, loginSessionNumber int64) (string, error) {
	oldLoginSession, dErr := c.generateLoginSession(id, clientIP, oldEmail, loginSessionNumber)
	if dErr != nil {
		return "", errors.Wrap(dErr, "can't generate loginSession")
	}

	return oldLoginSession, nil
}

func (c *client) validateEmailSignIn(ctx context.Context, id *loginID) error {
	gUsr, err := c.getEmailLinkSignIn(ctx, id, false)
	if err != nil && !storage.IsErr(err, storage.ErrNotFound) {
		return errors.Wrapf(err, "can't get email link sign in information by:%#v", id)
	}
	now := time.Now()
	if gUsr != nil {
		if gUsr.BlockedUntil != nil {
			if gUsr.BlockedUntil.After(*now.Time) {
				err = errors.Wrapf(ErrUserBlocked, "user:%#v is blocked due to a lot of incorrect codes", id)

				return terror.New(err, map[string]any{"source": "email"})
			}
		}
	}

	return nil
}

func (c *client) validateEmailModification(ctx context.Context, newEmail string, oldID *loginID) error {
	if iErr := c.isUserExist(ctx, newEmail); !storage.IsErr(iErr, storage.ErrNotFound) {
		if iErr != nil {
			return errors.Wrapf(iErr, "can't check if user exists for email:%v", newEmail)
		}

		return errors.Wrapf(terror.New(ErrUserDuplicate, map[string]any{"field": "email"}), "user with such email already exists:%v", newEmail)
	}
	gOldUsr, gErr := c.getEmailLinkSignIn(ctx, oldID, false)
	if gErr != nil && !storage.IsErr(gErr, storage.ErrNotFound) {
		return errors.Wrapf(gErr, "can't get email link sign in information by:%#v", oldID)
	}
	if gOldUsr != nil && gOldUsr.BlockedUntil != nil {
		now := time.Now()
		if gOldUsr.BlockedUntil.After(*now.Time) {
			err := errors.Wrapf(ErrUserBlocked, "user:%#v is blocked", oldID)

			return terror.New(err, map[string]any{"source": "email"})
		}
	}

	return nil
}

//nolint:funlen // .
func (c *client) sendEmailWithType(ctx context.Context, emailType, language string, toEmails, confirmationCodes []string) error {
	var tmpl *emailTemplate
	tmpl, ok := allEmailLinkTemplates[emailType][language]
	if !ok {
		tmpl = allEmailLinkTemplates[emailType][defaultLanguage]
	}
	dataBody := struct {
		Email            string
		ConfirmationCode string
		PetName          string
		AppName          string
		TeamName         string
	}{
		PetName:          c.cfg.PetName,
		AppName:          c.cfg.AppName,
		TeamName:         c.cfg.TeamName,
		Email:            "{{.Email}}",
		ConfirmationCode: "{{.ConfirmationCode}}",
	}
	dataSubject := struct {
		AppName string
	}{
		AppName: c.cfg.AppName,
	}
	participants := make([]email.Participant, 0, len(toEmails))
	for i := range toEmails {
		participants = append(participants, email.Participant{
			Name:               "",
			Email:              toEmails[i],
			SubstitutionFields: map[string]string{"{{.ConfirmationCode}}": confirmationCodes[i], "{{.Email}}": toEmails[i]},
		})
	}

	lbIdx := atomic.AddUint64(&c.emailClientLBIndex, 1) % uint64(c.cfg.ExtraLoadBalancersCount+1)

	return errors.Wrapf(c.emailClients[lbIdx].Send(ctx, &email.Parcel{
		Body: &email.Body{
			Type: email.TextHTML,
			Data: tmpl.getBody(dataBody),
		},
		Subject: tmpl.getSubject(dataSubject),
		From: email.Participant{
			Name:  c.fromRecipients[lbIdx].FromEmailName,
			Email: c.fromRecipients[lbIdx].FromEmailAddress,
		},
	}, participants...), "failed to send email with type:%v for user with emails:%v", emailType, toEmails)
}

//nolint:lll,revive // .
func (c *client) upsertEmailLinkSignIn(ctx context.Context, toEmail, deviceUniqueID, code, language string, now *time.Time) error {
	confirmationCodeWrongAttempts := 0
	params := []any{now.Time, toEmail, deviceUniqueID, code, language, confirmationCodeWrongAttempts, userIDForPhoneNumberToEmailMigration(ctx)}
	sql := fmt.Sprintf(`INSERT INTO email_link_sign_ins (
							created_at,
							email,
							device_unique_id,
							confirmation_code,
                            language,
							confirmation_code_wrong_attempts_count,
							phone_number_to_email_migration_user_id)
						VALUES ($1, $2, $3, $4,$5, $6, NULLIF($7,''))
						ON CONFLICT (email, device_unique_id) DO UPDATE 
							SET created_at    				     	   = EXCLUDED.created_at,
								confirmation_code 		          	   = EXCLUDED.confirmation_code,
								language 		          	           = EXCLUDED.language,
								confirmation_code_wrong_attempts_count = EXCLUDED.confirmation_code_wrong_attempts_count,
								phone_number_to_email_migration_user_id = COALESCE(NULLIF(EXCLUDED.phone_number_to_email_migration_user_id,''),email_link_sign_ins.phone_number_to_email_migration_user_id),
						        email_confirmed_at                     = null,
						        user_id                                = null
						WHERE   (extract(epoch from email_link_sign_ins.created_at)::bigint/%[1]v)  != (extract(epoch from EXCLUDED.created_at::timestamp)::bigint/%[1]v)
						   AND   (email_link_sign_ins.confirmation_code 		          	        != EXCLUDED.confirmation_code
						   OR   email_link_sign_ins.confirmation_code_wrong_attempts_count          != EXCLUDED.confirmation_code_wrong_attempts_count)`,
		uint64(duplicatedSignInRequestsInLessThan/stdlibtime.Second))
	rowsInserted, err := storage.Exec(ctx, c.db, sql, params...)
	if rowsInserted == 0 && err == nil {
		err = errors.Wrapf(ErrUserDuplicate, "duplicated signIn request for email %v,device %v", toEmail, deviceUniqueID)
	}

	return errors.Wrapf(err, "failed to insert/update email link sign ins record for email:%v", toEmail)
}

func (c *client) generateMagicLinkPayload(id *loginID, oldEmail string, now *time.Time) (string, error) {
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, loginFlowToken{
		RegisteredClaims: &jwt.RegisteredClaims{
			Issuer:    jwtIssuer,
			Subject:   id.Email,
			Audience:  nil,
			ExpiresAt: jwt.NewNumericDate(now.Add(c.cfg.EmailValidation.ExpirationTime)),
			NotBefore: jwt.NewNumericDate(*now.Time),
			IssuedAt:  jwt.NewNumericDate(*now.Time),
		},
		OldEmail:       oldEmail,
		DeviceUniqueID: id.DeviceUniqueID,
	})
	payload, err := token.SignedString([]byte(c.cfg.LoginSession.JwtSecret))
	if err != nil {
		return "", errors.Wrapf(err, "can't generate link payload for id:%#v,now:%v", id, now)
	}

	return payload, nil
}

func (c *client) generateLoginSession(id *loginID, clientIP, oldEmail string, loginSessionNumber int64) (string, error) {
	now := time.Now()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, loginFlowToken{
		RegisteredClaims: &jwt.RegisteredClaims{
			Issuer:    jwtIssuer,
			Subject:   id.Email,
			Audience:  nil,
			ExpiresAt: jwt.NewNumericDate(now.Add(c.cfg.EmailValidation.ExpirationTime)),
			NotBefore: jwt.NewNumericDate(*now.Time),
			IssuedAt:  jwt.NewNumericDate(*now.Time),
		},
		DeviceUniqueID:     id.DeviceUniqueID,
		LoginSessionNumber: loginSessionNumber,
		OldEmail:           oldEmail,
		NotifyEmail:        oldEmail,
		ClientIP:           clientIP,
	})
	payload, err := token.SignedString([]byte(c.cfg.LoginSession.JwtSecret))
	if err != nil {
		return "", errors.Wrapf(err, "can't generate login flow for id:%#v,now:%v", id, now)
	}

	return payload, nil
}

func generateConfirmationCode() string {
	result, err := rand.Int(rand.Reader, big.NewInt(999)) //nolint:gomnd // It's max value.
	log.Panic(err, "random wrong")

	return fmt.Sprintf("%03d", result.Int64()+1)
}
