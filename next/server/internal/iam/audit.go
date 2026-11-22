package iam

import (
	"context"
	"strconv"

	"gorm.io/gorm"

	"github.com/Sub2API-Devs/sup2api/next/server/internal/audit"
)

// auditUser records, in q, that actorID did action to user userID: every
// audit row of this package is about a user.
func auditUser(ctx context.Context, q *gorm.DB, actorID int64, action string, userID int64, detail any) error {
	return audit.Audit(ctx, q, actorID, action, "user", strconv.FormatInt(userID, 10), detail)
}

// auditSelf records that user userID did action to themselves.
func auditSelf(ctx context.Context, q *gorm.DB, userID int64, action string, detail any) error {
	return auditUser(ctx, q, userID, action, userID, detail)
}
