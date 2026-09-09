// Manual supplier merge (合并重复供应商): the cleanup companion to the
// entry-time duplicate check. Dedup PREVENTS new duplicates (manual form /
// Excel import / MCP); merge resolves duplicates that already exist in the
// library — two documents for the same real-world company accumulated before
// dedup existed, or across people.
//
// The operation is deliberately conservative and master-directed:
//   - the MASTER keeps its identity (basic_info, owner, visibility, status,
//     risk verdict) and absorbs the duplicate's additive collections;
//   - performance history, attachments, qualifications, categories, product
//     lines and custom fields are unioned in (master wins on conflict);
//   - the DUPLICATE is archived (history retained), never hard-deleted;
//   - a blacklisted record on either side refuses the merge — a confirmed
//     fraud company must not be silently laundered into a clean one. The
//     reviewer resolves the blacklist or keeps the records separate.
//
// Attachments are reference-unioned (the duplicate's attachment records are
// copied onto the master). Their files stay where they were: the download
// path resolves the owning supplier from the object key and still reads an
// archived document, so no bytes are moved.
package supplier

import (
	"context"
	"fmt"
	"strings"

	"github.com/supplider/supplider/backend/internal/datamodel"
	"github.com/supplider/supplider/backend/internal/domain"
)

// MergeResult reports what a merge consolidated.
type MergeResult struct {
	MasterID          string `json:"master_id"`
	DuplicateID       string `json:"duplicate_id"`
	PerformanceAdded  int    `json:"performance_added"`
	AttachmentsAdded  int    `json:"attachments_added"`
	QualsAdded        int    `json:"qualifications_added"`
	CategoriesAdded   int    `json:"categories_added"`
	ProductsAdded     int    `json:"products_added"`
	CustomFieldsAdded int    `json:"custom_fields_added"`
}

// MergeSuppliers consolidates the duplicate into the master and archives the
// duplicate. Both must be LIVE (not archived) and neither may be blacklisted;
// merging a record into itself is rejected. Returns the updated master.
func (s *Service) MergeSuppliers(ctx context.Context, masterID, duplicateID string) (*domain.Supplier, MergeResult, error) {
	masterID = strings.TrimSpace(masterID)
	duplicateID = strings.TrimSpace(duplicateID)
	if masterID == "" || duplicateID == "" {
		return nil, MergeResult{}, fmt.Errorf("merge: both master id and duplicate id are required")
	}
	if masterID == duplicateID {
		return nil, MergeResult{}, fmt.Errorf("merge: master and duplicate must be different suppliers")
	}

	master, err := s.store.Get(ctx, masterID)
	if err != nil {
		return nil, MergeResult{}, err
	}
	dup, err := s.store.Get(ctx, duplicateID)
	if err != nil {
		return nil, MergeResult{}, err
	}

	if master.Status == domain.StatusArchived || dup.Status == domain.StatusArchived {
		return nil, MergeResult{}, fmt.Errorf("merge: suppliers must be live (not archived) to be merged — restore before merging (master=%s status=%s duplicate=%s status=%s)",
			masterID, master.Status, duplicateID, dup.Status)
	}
	if master.Status == domain.StatusBlacklisted || dup.Status == domain.StatusBlacklisted {
		return nil, MergeResult{}, fmt.Errorf("merge: both suppliers must be non-blacklisted and live to be merged (a 黑名单 record must never be folded into another; resolve the blacklist or keep them separate)")
	}

	now := s.now()
	res := MergeResult{MasterID: masterID, DuplicateID: duplicateID}

	// --- union collections into the master (master wins on conflict) ---

	// Categories (string set).
	catSeen := stringSet(master.Categories)
	for _, c := range dup.Categories {
		if c = strings.TrimSpace(c); c != "" && !catSeen[c] {
			catSeen[c] = true
			master.Categories = append(master.Categories, c)
			res.CategoriesAdded++
		}
	}

	// Qualifications: same cert number (both sides numbered), or same
	// type+level when at least one side lacks a number, counts as the same
	// certificate. When the duplicate carries a cert number / dates the
	// master record lacks, fill the empty fields in place — dropping the
	// numbered copy outright would lose the cert number and its expiry (the
	// 90/30/7-day reminders key off it). Master values always win conflicts.
	for _, q := range dup.Qualifications {
		if idx := qualIndex(master.Qualifications, q); idx >= 0 {
			enrichQualification(&master.Qualifications[idx], q)
			continue
		}
		master.Qualifications = append(master.Qualifications, q)
		res.QualsAdded++
	}

	// Product/service lines: union by name.
	prodSeen := map[string]bool{}
	for _, p := range master.ProductsServices {
		prodSeen[strings.TrimSpace(p.Name)] = true
	}
	for _, p := range dup.ProductsServices {
		if name := strings.TrimSpace(p.Name); name != "" && !prodSeen[name] {
			prodSeen[name] = true
			master.ProductsServices = append(master.ProductsServices, p)
			res.ProductsAdded++
		}
	}

	// Performance history: append the duplicate's collaborations (dedup by
	// project+date), then recompute the aggregate rating.
	perfSeen := map[string]bool{}
	for _, p := range master.PerformanceHistory {
		perfSeen[perfKey(p)] = true
	}
	for _, p := range dup.PerformanceHistory {
		if k := perfKey(p); !perfSeen[k] {
			perfSeen[k] = true
			master.PerformanceHistory = append(master.PerformanceHistory, p)
			res.PerformanceAdded++
		}
	}

	// Attachments: reference-union (files stay in place; URLs still resolve).
	attSeen := map[string]bool{}
	for _, a := range master.Attachments {
		attSeen[a.URL] = true
	}
	for _, a := range dup.Attachments {
		if a.URL != "" && !attSeen[a.URL] {
			attSeen[a.URL] = true
			master.Attachments = append(master.Attachments, a)
			res.AttachmentsAdded++
		}
	}

	// Custom fields: master wins — only keys the master does not set are
	// carried over (the duplicate's value for a shared key is discarded).
	if master.CustomFields == nil {
		master.CustomFields = map[string]any{}
	}
	for k, v := range dup.CustomFields {
		if _, exists := master.CustomFields[k]; !exists {
			master.CustomFields[k] = v
			res.CustomFieldsAdded++
		}
	}

	// Shared visibility allow-list (level 1 named users): union the ids.
	sharedSeen := stringSet(master.SharedWith)
	for _, u := range dup.SharedWith {
		if u = strings.TrimSpace(u); u != "" && !sharedSeen[u] {
			sharedSeen[u] = true
			master.SharedWith = append(master.SharedWith, u)
		}
	}

	recomputeRating(master)

	master.ChangeLog = append(master.ChangeLog, domain.ChangeEntry{
		Field: "merged_from",
		New: fmt.Sprintf("%s（%s）：并入绩效 %d、附件 %d、资质 %d、品类 %d、产品线 %d、自定义字段 %d",
			dup.ID, dup.BasicInfo.CompanyName,
			res.PerformanceAdded, res.AttachmentsAdded, res.QualsAdded,
			res.CategoriesAdded, res.ProductsAdded, res.CustomFieldsAdded),
		Date:   now,
		Source: domain.SourceManual,
	})
	master.UpdatedAt = now
	if err := s.store.Put(ctx, master); err != nil {
		return nil, MergeResult{}, err
	}

	// Record the pointer on the duplicate, then archive it (reuses the
	// tested archive path: status=archived, history retained, hidden from
	// the default list).
	dup.ChangeLog = append(dup.ChangeLog, domain.ChangeEntry{
		Field:  "merged_into",
		New:    fmt.Sprintf("%s（%s）", master.ID, master.BasicInfo.CompanyName),
		Date:   now,
		Source: domain.SourceManual,
	})
	dup.UpdatedAt = now
	if err := s.store.Put(ctx, dup); err != nil {
		return nil, MergeResult{}, err
	}
	if err := s.store.Delete(ctx, duplicateID); err != nil {
		return nil, MergeResult{}, err
	}

	// Followers of the master learn it absorbed a duplicate; followers of
	// the (now archived) duplicate learn where their record went.
	s.notify(ctx, master, datamodel.NotifMerged, datamodel.SeverityInfo,
		"关注供应商合并了重复档案",
		fmt.Sprintf("已将重复档案「%s」并入本供应商（绩效 %d、资质 %d、附件 %d 条）",
			dup.BasicInfo.CompanyName, res.PerformanceAdded, res.QualsAdded, res.AttachmentsAdded), "")
	s.notify(ctx, dup, datamodel.NotifMergedAway, datamodel.SeverityWarning,
		"关注供应商已并入其他档案并归档",
		fmt.Sprintf("该供应商已并入「%s」（%s）并归档，历史仍可查",
			master.BasicInfo.CompanyName, master.ID), "")

	return master, res, nil
}

// ---- consolidation helpers ----

func stringSet(xs []string) map[string]bool {
	m := map[string]bool{}
	for _, x := range xs {
		if x = strings.TrimSpace(x); x != "" {
			m[x] = true
		}
	}
	return m
}

// qualIndex returns the index of the qualification in existing that is the
// same certificate as q, or -1. When both carry a certificate number only an
// equal (case-insensitive) number matches; with fewer than two numbers the
// fallback is same type+level (an unnumbered record of a later-numbered cert
// still represents the same qualification line).
func qualIndex(existing []domain.Qualification, q domain.Qualification) int {
	for i, e := range existing {
		if strings.TrimSpace(q.CertNo) != "" && strings.TrimSpace(e.CertNo) != "" {
			if strings.EqualFold(strings.TrimSpace(e.CertNo), strings.TrimSpace(q.CertNo)) {
				return i
			}
			continue
		}
		if strings.TrimSpace(e.Type) == strings.TrimSpace(q.Type) &&
			strings.TrimSpace(e.Level) == strings.TrimSpace(q.Level) {
			return i
		}
	}
	return -1
}

// enrichQualification fills empty fields of a master qualification from the
// duplicate's matching record (cert number, issuer, dates, verified flag).
// Existing master values always win; this only prevents losing data the
// master never recorded — especially the expiry driving renewal reminders.
func enrichQualification(master *domain.Qualification, dup domain.Qualification) {
	if strings.TrimSpace(master.CertNo) == "" {
		master.CertNo = strings.TrimSpace(dup.CertNo)
	}
	if strings.TrimSpace(master.Issuer) == "" {
		master.Issuer = strings.TrimSpace(dup.Issuer)
	}
	if strings.TrimSpace(master.IssueDate) == "" {
		master.IssueDate = strings.TrimSpace(dup.IssueDate)
	}
	if strings.TrimSpace(master.Expiry) == "" {
		master.Expiry = strings.TrimSpace(dup.Expiry)
	}
	if !master.Verified && dup.Verified {
		master.Verified = true
	}
}

func perfKey(p domain.Performance) string {
	project := strings.TrimSpace(p.Project)
	date := strings.TrimSpace(p.Date)
	if project != "" || date != "" {
		return project + "|" + date
	}
	// Score-only records are legal (validation only bounds the scores), so an
	// empty project+date cannot be the identity: distinct evaluations without
	// a project name would all collapse onto "|" and be silently dropped on
	// merge. Fall back to a content fingerprint — different scores/feedback
	// survive, byte-identical double entries still dedup (keeps a retried
	// merge idempotent).
	return fmt.Sprintf("anon|score=%g|delivery=%g|quality=%g|cooperation=%g|feedback=%s",
		p.Score, p.Delivery, p.Quality, p.Cooperation, strings.TrimSpace(p.Feedback))
}
