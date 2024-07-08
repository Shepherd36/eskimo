// SPDX-License-Identifier: ice License 1.0

package emaillinkiceauth

import (
	"context"
	"fmt"
	"math"
	"math/rand/v2"
	"strconv"
	"strings"
	stdlibtime "time"

	"github.com/hashicorp/go-multierror"
	"github.com/pkg/errors"
	"github.com/redis/go-redis/v9"

	"github.com/ice-blockchain/wintr/connectors/storage/v2"
	"github.com/ice-blockchain/wintr/email"
	"github.com/ice-blockchain/wintr/log"
	"github.com/ice-blockchain/wintr/time"
)

//nolint:funlen // .
func (c *client) enqueueLoginAttempt(ctx context.Context, now *time.Time, userEmail string) (queuePosition int64, rateLimit string, err error) {
	var result []redis.Cmder
	result, err = c.queueDB.TxPipelined(ctx, func(pipeliner redis.Pipeliner) error {
		if zErr := pipeliner.ZAddNX(ctx, loginQueueKey, redis.Z{
			Score:  float64(now.Nanosecond()),
			Member: userEmail,
		}).Err(); zErr != nil {
			return zErr //nolint:wrapcheck // .
		}
		if zRankErr := pipeliner.ZRank(ctx, loginQueueKey, userEmail).Err(); zRankErr != nil {
			return zRankErr //nolint:wrapcheck // .
		}

		return pipeliner.Get(ctx, loginRateLimitKey).Err()
	})
	if err != nil {
		return 0, "", errors.Wrapf(err, "failed to enqueue email")
	}
	errs := make([]error, 0, len(result))
	for idx := 2; idx >= 0; idx-- {
		cmdRes := result[idx]
		if cmdRes.Err() != nil {
			errs = append(errs, errors.Wrapf(cmdRes.Err(), "failed to enqueue email because of failed %v", cmdRes.String()))

			continue
		}
		switch idx {
		case 2: //nolint:gomnd // Index in pipeline.
			strCmd := cmdRes.(*redis.StringCmd) //nolint:errcheck,forcetypeassert // .
			rateLimit = strCmd.Val()
		case 1:
			intCmd := cmdRes.(*redis.IntCmd) //nolint:errcheck,forcetypeassert // .
			queuePosition = intCmd.Val() + 1
		case 0:
			intCmd := cmdRes.(*redis.IntCmd) //nolint:errcheck,forcetypeassert // .
			if intCmd.Val() == 0 {
				return queuePosition, rateLimit, errAlreadyEnqueued
			}
		}
	}
	if cmdErr := multierror.Append(nil, errs...).ErrorOrNil(); cmdErr != nil {
		return queuePosition, rateLimit, errors.Wrapf(cmdErr, "failed to enqueue email %v", userEmail)
	}

	return queuePosition, rateLimit, nil
}

//nolint:funlen,gocognit,revive,contextcheck // Keep processing in signle place.
func (c *client) processEmailQueue(rootCtx context.Context) {
	lastProcessed := time.Now()

	emailQueueLock := storage.NewMutex(c.db, loginQueueKey)
	lockCtx, lockCancel := context.WithTimeout(context.Background(), 30*stdlibtime.Second) //nolint:gomnd // .
	if err := emailQueueLock.Lock(lockCtx); err != nil {
		if !errors.Is(err, storage.ErrMutexNotLocked) {
			log.Panic(errors.Wrapf(err, "failed to obtain emailQueueLock for email queue"))
		}
	}
	lockCancel()
	c.queueWg.Add(1)
	defer func() {
		log.Error(errors.Wrapf(c.queueDB.Close(), "failed to close email queue db"))
		c.queueWg.Done()
	}()
	for rootCtx.Err() == nil {
		now := time.Now()
		reqCtx, reqCancel := context.WithTimeout(context.Background(), 30*stdlibtime.Second) //nolint:gomnd // .
		if lockErr := emailQueueLock.EnsureLocked(reqCtx); lockErr != nil {
			reqCancel()
			if errors.Is(lockErr, storage.ErrTxAborted) {
				return
			}
			if errors.Is(lockErr, storage.ErrMutexNotLocked) {
				_ = wait(rootCtx, stdlibtime.Duration(1+rand.IntN(4))*stdlibtime.Second) //nolint:errcheck,gosec,gomnd // Nothing to rollback.

				continue
			}
		}
		reqCancel()
		reqCtx, reqCancel = context.WithTimeout(context.Background(), 30*stdlibtime.Second) //nolint:gomnd // .
		emails, scores, rateLimit, err := c.dequeueNextEmails(reqCtx)                       //nolint:contextcheck // Background context.
		if err != nil {
			log.Error(errors.Wrapf(err, "failed to fetch next %v emails in queue", email.MaxBatchSize))
			reqCancel()
			_ = wait(rootCtx, 1*stdlibtime.Second) //nolint:errcheck // Noting to rollback.

			continue
		}
		reqCancel()

		if len(emails) == 0 {
			log.Info("No emails in queue for sending")
			_ = wait(rootCtx, 10*stdlibtime.Second) //nolint:errcheck,gomnd // Nothing to rollback.

			continue
		}

		rlCount, rlDuration, rlErr := parseRateLimit(rateLimit)
		if rlErr != nil {
			log.Error(errors.Wrapf(c.rollbackEmailsBackToQueue(emails, scores), "failed to rollback emails %#v back to queue", emails))
			log.Panic(errors.Wrapf(rlErr, "failed to parse rate limit for email queue %v", rateLimit)) //nolint:revive // .
		}
		limit := int(math.Min(float64(rlCount), float64(len(emails))))
		if rlCount < len(emails) {
			log.Error(errors.Wrapf(c.rollbackEmailsBackToQueue(emails[rlCount:], scores), "failed to rollback emails %#v back to queue cuz rate limit %v is less than batch %v", emails[rlCount:], rlCount, email.MaxBatchSize)) //nolint:lll // .
			emails = emails[:rlCount]
		}

		reqCtx, reqCancel = context.WithTimeout(context.Background(), 30*stdlibtime.Second) //nolint:gomnd // .
		loginInformation, err := c.fetchLoginInformationForEmailBatch(reqCtx, now, emails, limit)
		if err != nil {
			log.Error(errors.Wrapf(err, "failed to fetch login information for emails: %v", emails))
			reqCancel()
			log.Error(errors.Wrapf(c.rollbackEmailsBackToQueue(emails, scores), "failed to rollback emails %#v back to queue", emails))
			_ = wait(rootCtx, 1*stdlibtime.Second) //nolint:errcheck // Already rolled back.

			continue
		}
		reqCancel()
		lastTimeBatchProcessingDuration := time.Now().Sub(*lastProcessed.Time)
		rateLimitEstimationDuration := lastTimeBatchProcessingDuration * stdlibtime.Duration(int64(rlCount)/int64(len(emails)))
		if rateLimitEstimationDuration < rlDuration {
			oneBatchProcessingTimeToRespectRateLimit := stdlibtime.Duration(int64(len(emails))/int64(rlCount)) * rlDuration
			if wait(rootCtx, oneBatchProcessingTimeToRespectRateLimit) != nil {
				log.Error(errors.Wrapf(c.rollbackEmailsBackToQueue(emails, scores), "failed to rollback fetched emails %#v back to queue", emails))

				continue
			}
		}
		reqCtx, reqCancel = context.WithTimeout(context.Background(), 30*stdlibtime.Second) //nolint:gomnd // .
		if failed, sErr := c.sendEmails(reqCtx, loginInformation); sErr != nil {
			reqCancel()
			log.Error(errors.Wrapf(sErr, "failed to send email batch for emails %#v", failed))
			log.Error(errors.Wrapf(c.rollbackEmailsBackToQueue(failed, scores), "failed to rollback failed emails %#v back to queue", failed))
			stdlibtime.Sleep(1 * stdlibtime.Second)

			continue
		}
		reqCancel()
		lastProcessed = time.Now()
	}
}

func (c *client) rollbackEmailsBackToQueue(failed []string, scores map[string]int64) error {
	rollbackCtx, rollbackCancel := context.WithTimeout(context.Background(), 30*stdlibtime.Second) //nolint:gomnd // .
	defer rollbackCancel()
	failedZ := make([]redis.Z, 0, len(failed))
	for _, failedEmail := range failed {
		failedZ = append(failedZ, redis.Z{
			Score:  float64(scores[failedEmail]),
			Member: failedEmail,
		})
	}

	return errors.Wrapf(c.queueDB.ZAddNX(rollbackCtx, loginQueueKey, failedZ...).Err(), "failed to rollback unsent emails %#v", failed)
}

//nolint:gocritic,revive // We need all the results from the pipeline
func (c *client) dequeueNextEmails(ctx context.Context) (emailsBatch []string, scores map[string]int64, rateLimit string, err error) {
	var pipeRes []redis.Cmder
	pipeRes, err = c.queueDB.TxPipelined(ctx, func(pipeliner redis.Pipeliner) error {
		if zpopErr := pipeliner.ZPopMin(ctx, loginQueueKey, email.MaxBatchSize).Err(); zpopErr != nil {
			return zpopErr //nolint:wrapcheck // .
		}

		return pipeliner.Get(ctx, loginRateLimitKey).Err()
	})
	if err != nil {
		return nil, nil, "", errors.Wrapf(err, "failed to fetch email queue batch")
	}
	if zpopErr := pipeRes[0].Err(); zpopErr != nil {
		return nil, nil, "", errors.Wrapf(zpopErr, "failed to fetch %v email queue batch", pipeRes[0].String())
	}
	if len(pipeRes) > 1 {
		if rateErr := pipeRes[1].Err(); rateErr != nil {
			return nil, nil, "", errors.Wrapf(rateErr, "failed to fetch %v email sending rate", pipeRes[1].String())
		}
	}
	batch := pipeRes[0].(*redis.ZSliceCmd).Val() //nolint:forcetypeassert // .
	emailsBatch = make([]string, 0, len(batch))
	scores = make(map[string]int64, 0)
	for _, itemInBatch := range batch {
		emailsBatch = append(emailsBatch, itemInBatch.Member.(string)) //nolint:forcetypeassert // .
		scores[emailsBatch[len(emailsBatch)-1]] = int64(itemInBatch.Score)
	}
	rate := pipeRes[1].(*redis.StringCmd).Val() //nolint:forcetypeassert // .

	return emailsBatch, scores, rate, nil
}

func (c *client) fetchLoginInformationForEmailBatch(ctx context.Context, now *time.Time, emails []string, limit int) ([]*emailLinkSignIn, error) {
	sql := fmt.Sprintf(`
		 SELECT * FROM public.email_link_sign_ins 
         WHERE email = ANY($1) AND created_at > ($2::TIMESTAMP - (%[2]v * interval '1 second'))
         ORDER BY created_at DESC
         LIMIT %[1]v;`, limit, c.cfg.EmailValidation.ExpirationTime.Seconds())
	res, err := storage.Select[emailLinkSignIn](ctx, c.db, sql, emails, now.Time)

	return res, err
}

func parseRateLimit(rateLimit string) (int, stdlibtime.Duration, error) {
	spl := strings.Split(rateLimit, ":")
	rateLimitCount, rlErr := strconv.Atoi(spl[0])
	if rlErr != nil {
		return 0, stdlibtime.Duration(0), rlErr
	}
	rateLimitDuration, rlErr := stdlibtime.ParseDuration(spl[1])
	if rlErr != nil {
		return 0, stdlibtime.Duration(0), rlErr
	}

	return rateLimitCount, rateLimitDuration, nil
}

func (c *client) sendEmails(ctx context.Context, emails []*emailLinkSignIn) (failed []string, err error) {
	emailsByLanguage := make(map[string][]string)
	confCodesByLanguage := make(map[string][]string)
	for _, userEmail := range emails {
		emailsByLanguage[userEmail.Language] = append(emailsByLanguage[userEmail.Language], userEmail.Email)
		confCodesByLanguage[userEmail.Language] = append(confCodesByLanguage[userEmail.Language], userEmail.ConfirmationCode)
	}
	var mErr *multierror.Error
	for language := range emailsByLanguage {
		if sErr := c.sendEmailWithType(ctx, signInEmailType, language, emailsByLanguage[language], confCodesByLanguage[language]); sErr != nil {
			mErr = multierror.Append(mErr, errors.Wrapf(sErr, "failed to send emails for language %v: %#v", language, emailsByLanguage[language]))
			failed = append(failed, emailsByLanguage[language]...)
		}
	}

	return failed, mErr.ErrorOrNil() //nolint:wrapcheck // .
}

func wait(ctx context.Context, d stdlibtime.Duration) error {
	select {
	case <-stdlibtime.After(d):
		return nil
	case <-ctx.Done():
		log.Info("cancelled")

		return context.Canceled
	}
}
