-- SPDX-License-Identifier: ice License 1.0

CREATE TABLE IF NOT EXISTS users_forwarded_to_face_kyc
(
    forwarded_at   TIMESTAMP NOT NULL,
    user_id        TEXT    not null references users (id) ON DELETE CASCADE,
    primary key (user_id)
);
CREATE INDEX IF NOT EXISTS users_forwarded_to_face_kyc_forwarded_at_ix ON users_forwarded_to_face_kyc (forwarded_at);