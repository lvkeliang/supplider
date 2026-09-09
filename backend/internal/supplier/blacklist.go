// Supplier blacklist (黑名单): the 淘汰 phase "do not use" state. Where
// archive is delete-with-history (the supplier leaves normal use quietly),
// the blacklist is an explicit, visible warning — a blacklisted supplier
// STILL appears in list and search results so nobody accidentally selects a
// confirmed-fraud / serious-breach company, but is badged everywhere.
//
// Blacklist is a normal live status (status=blacklisted), NOT the archived
// state: it is set with Put (store.Delete forces archived). Risk detection
// feeds the decision (a confirmed shell/fraud → blacklist), but blacklisting
// is a human lifecycle action recorded in change_log.
package supplier

import (
	"context"
	"fmt"
	"strings"

	"github.com/supplider/supplider/backend/internal/datamodel"
	"github.com/supplider/supplider/backend/internal/domain"
)

// Blacklist moves a live supplier onto the blacklist with a human-supplied
// reason (淘汰/黑名单). Idempotent: an already-blacklisted supplier is
// returned unchanged. Archived suppliers must be restored first (blacklist is
// a live, visible state). The status change and reason go to change_log.
func (s *Service) Blacklist(ctx context.Context, id, reason string) (*domain.Supplier, error) {
	doc, err := s.store.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if doc.Status == domain.StatusArchived {
		return nil, fmt.Errorf("supplier: %s is archived and must be restored before blacklisting", id)
	}
	if doc.Status == domain.StatusBlacklisted {
		return doc, nil // idempotent
	}

	now := s.now()
	prevStatus := doc.Status
	prevReason := doc.BlacklistReason
	reason = strings.TrimSpace(reason)

	doc.Status = domain.StatusBlacklisted
	doc.BlacklistReason = reason
	doc.ChangeLog = append(doc.ChangeLog, domain.ChangeEntry{
		Field: "status", Old: prevStatus, New: domain.StatusBlacklisted, Date: now, Source: domain.SourceManual,
	})
	if reason != "" {
		doc.ChangeLog = append(doc.ChangeLog, domain.ChangeEntry{
			Field: "blacklist_reason", Old: prevReason, New: reason, Date: now, Source: domain.SourceManual,
		})
	}
	doc.UpdatedAt = now
	if err := s.store.Put(ctx, doc); err != nil {
		return nil, err
	}
	body := "该供应商已被列入黑名单，请勿选用"
	if reason != "" {
		body = "该供应商已被列入黑名单，请勿选用。原因：" + reason
	}
	s.notify(ctx, doc, datamodel.NotifBlacklisted, datamodel.SeverityDanger,
		"关注供应商已列入黑名单", body, "")
	return doc, nil
}

// Unblacklist removes a supplier from the blacklist, returning it to active.
// Idempotent: a non-blacklisted supplier is returned unchanged.
func (s *Service) Unblacklist(ctx context.Context, id string) (*domain.Supplier, error) {
	doc, err := s.store.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if doc.Status != domain.StatusBlacklisted {
		return doc, nil // no-op
	}

	now := s.now()
	doc.Status = domain.StatusActive
	doc.BlacklistReason = ""
	doc.ChangeLog = append(doc.ChangeLog, domain.ChangeEntry{
		Field: "status", Old: domain.StatusBlacklisted, New: domain.StatusActive, Date: now, Source: domain.SourceManual,
	})
	doc.UpdatedAt = now
	if err := s.store.Put(ctx, doc); err != nil {
		return nil, err
	}
	s.notify(ctx, doc, datamodel.NotifUnblacklisted, datamodel.SeverityInfo,
		"关注供应商已移出黑名单", "该供应商已恢复为正常在库状态", "")
	return doc, nil
}
