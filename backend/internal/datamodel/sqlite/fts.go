// SQLite FTS5 full-text index — the personal-tier search engine.
//
// This is the SQLite-internal transition implementation of the search
// port (internal/search); embedded Meilisearch / Elasticsearch later
// implement search.Index directly WITHOUT business-code changes. FTS5
// lives in the SAME database file as the supplier rows and is maintained
// in the SAME transaction as each Put, so there is no dual-write window
// and a personal backup stays one file.
//
// Tokenizer choice: trigram (overlapping 3-character sequences).
//
//   - Chinese needs no segmentation dictionary: "杭州一建有限公司" is
//     searchable by any substring of ≥3 characters (杭州一建, 州一建有,
//     一建有限…) — the common case (multi-char company/category names).
//   - Latin/digits get typos-tolerant partial matching (credit codes,
//     pinyin).
//   - Trigram MATCH cannot match terms shorter than 3 characters; those
//     ("水泥", "建材", "杭州"…) fall back to LIKE on search_text — see
//     keywordPredicates. Both paths are AND-ed with the structured
//     filters, so behavior is a strict superset of the old substring
//     search.
//
// Columns:
//
//   - content: the same searchable field blob as search_text PLUS
//     index-time synonym expansions (商砼 ↔ 混凝土…), so a document
//     written with one synonym is found by any other.
//   - py: pinyin forms (full concatenated syllables + initials, one
//     space-separated token per field) produced by the engine-agnostic
//     internal/search analyzer, enabling "hzyj" / "hangzhou" lookups.
package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/supplider/supplider/backend/internal/domain"
	"github.com/supplider/supplider/backend/internal/search"
)

// ftsSchema is appended to the v1 migration DDL (idempotent: databases
// created before FTS existed get the virtual table on next open and are
// backfilled by reindexFTS).
const ftsSchema = `
CREATE VIRTUAL TABLE IF NOT EXISTS suppliers_fts USING fts5(
    doc_id  UNINDEXED,
    content,
    py,
    tokenize = 'trigram'
);
`

// minTrigramRunes is the FTS5 trigram tokenizer's minimum phrase length:
// terms shorter than this are not indexable and go through LIKE instead.
const minTrigramRunes = 3

// reindexFTS backfills FTS rows for documents missing from the index
// (databases created before the FTS table existed). Runs once per Open;
// equal counts are the fast common path.
func (s *Store) reindexFTS(ctx context.Context) error {
	var supCnt, ftsCnt int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM suppliers`).Scan(&supCnt); err != nil {
		return fmt.Errorf("sqlite: fts backfill count: %w", err)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM suppliers_fts`).Scan(&ftsCnt); err != nil {
		return fmt.Errorf("sqlite: fts backfill count: %w", err)
	}
	if supCnt == ftsCnt {
		return nil
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT s.id, json(s.doc) FROM suppliers s
		 WHERE NOT EXISTS (SELECT 1 FROM suppliers_fts f WHERE f.doc_id = s.id)`)
	if err != nil {
		return fmt.Errorf("sqlite: fts backfill scan: %w", err)
	}
	type docRow struct{ id, docText string }
	var missing []docRow
	for rows.Next() {
		var r docRow
		if err := rows.Scan(&r.id, &r.docText); err != nil {
			rows.Close()
			return fmt.Errorf("sqlite: fts backfill scan: %w", err)
		}
		missing = append(missing, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("sqlite: fts backfill scan: %w", err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqlite: fts backfill tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	for _, r := range missing {
		doc, err := decode(r.docText, r.id)
		if err != nil {
			return err
		}
		content, py := ftsVectors(doc)
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO suppliers_fts (doc_id, content, py) VALUES (?, ?, ?)`,
			doc.ID, content, py); err != nil {
			return fmt.Errorf("sqlite: fts backfill insert %s: %w", doc.ID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sqlite: fts backfill commit: %w", err)
	}
	return nil
}

// syncFTS replaces a document's FTS row inside the Put transaction.
func syncFTS(ctx context.Context, tx *sql.Tx, doc *domain.Supplier) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM suppliers_fts WHERE doc_id = ?`, doc.ID); err != nil {
		return fmt.Errorf("sqlite: clear fts %s: %w", doc.ID, err)
	}
	content, py := ftsVectors(doc)
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO suppliers_fts (doc_id, content, py) VALUES (?, ?, ?)`,
		doc.ID, content, py); err != nil {
		return fmt.Errorf("sqlite: insert fts %s: %w", doc.ID, err)
	}
	return nil
}

// ftsVectors builds the two indexed texts for one document. content is
// the same searchable blob as search_text (field text + synonym
// expansions — see buildSearchText); py holds the pinyin forms.
func ftsVectors(d *domain.Supplier) (content, py string) {
	content = buildSearchText(d)

	var b strings.Builder
	addPy := func(text string) {
		if strings.TrimSpace(text) == "" {
			return
		}
		full, initials := search.Pinyin(text)
		if full != "" {
			b.WriteString(full)
			b.WriteByte(' ')
		}
		if initials != "" {
			b.WriteString(initials)
			b.WriteByte(' ')
		}
	}
	addPy(d.BasicInfo.CompanyName)
	addPy(d.BasicInfo.LegalPerson)
	addPy(d.BasicInfo.BusinessScope)
	for _, c := range d.Categories {
		addPy(c)
	}
	for _, p := range d.ProductsServices {
		addPy(p.Name)
	}
	for k, v := range d.CustomFields {
		addPy(k)
		addPy(fmt.Sprint(v))
	}
	py = strings.TrimSpace(b.String())
	return content, py
}

// keywordPredicates appends keyword-search predicates to a WHERE clause.
// Terms of ≥3 runes go through FTS5 MATCH (trigram substring over BOTH
// content and pinyin columns; table-level MATCH hits any column); shorter
// terms use LIKE on search_text (the trigram tokenizer cannot index
// sub-3-character phrases). All predicates AND together, matching the
// memory adapter's all-terms-must-match semantics.
func keywordPredicates(where []string, args []any, keyword string) ([]string, []any) {
	terms := search.SplitTerms(keyword)

	// likeOnSearchText emits a case-insensitive substring predicate against
	// s.search_text and binds the escaped literal. The coordination branch
	// needs several of these; the ordinary paths use it for short terms.
	likeOnSearchText := func(lit string) string {
		args = append(args, "%"+escapeLike(lit)+"%")
		return "LOWER(s.search_text) LIKE LOWER(?) ESCAPE '\\'"
	}

	// Fast path: when no term needs concatenated-CJK coordination, keep the
	// original shape — one combined MATCH (implicit AND over all long
	// phrases) plus LIKE for short terms — so ordinary queries are unchanged.
	needCoord := false
	for _, t := range terms {
		if _, ok := search.CJKCoordination(t); ok {
			needCoord = true
			break
		}
	}
	if !needCoord {
		var longPhrases []string
		for _, t := range terms {
			if utf8.RuneCountInString(t) >= minTrigramRunes {
				longPhrases = append(longPhrases, ftsQuote(t))
			} else {
				where = append(where, likeOnSearchText(t))
			}
		}
		if len(longPhrases) > 0 {
			where = append(where,
				"s.id IN (SELECT doc_id FROM suppliers_fts WHERE suppliers_fts MATCH ?)")
			args = append(args, strings.Join(longPhrases, " "))
		}
		return where, args
	}

	// Per-term path: each term is (exact match OR bigram-coordination), and
	// all terms AND together. Exact uses the FTS trigram index for long
	// terms and LIKE for short ones; the coordination fallback finds a
	// space-free CJK run (e.g. 杭州混凝土) whose concepts live in different
	// fields, mirroring search.TermMatchesText: bigram coverage, a front
	// anchor (one of the leading bigrams), the trailing bigram, and every
	// ASCII identifier fragment contiguously.
	for _, t := range terms {
		var exact string
		if utf8.RuneCountInString(t) >= minTrigramRunes {
			exact = "s.id IN (SELECT doc_id FROM suppliers_fts WHERE suppliers_fts MATCH ?)"
			args = append(args, ftsQuote(t))
		} else {
			exact = likeOnSearchText(t)
		}
		termParts := []string{exact}

		if plan, ok := search.CJKCoordination(t); ok {
			var cases []string
			for _, bg := range plan.Bigrams {
				cases = append(cases,
					"(CASE WHEN "+likeOnSearchText(bg)+" THEN 1 ELSE 0 END)")
			}
			coverage := "(" + strings.Join(cases, " + ") + " >= ?)"
			args = append(args, plan.MinHits)

			var fronts []string
			for _, bg := range plan.Front {
				fronts = append(fronts, likeOnSearchText(bg))
			}
			coordAnds := []string{coverage, "(" + strings.Join(fronts, " OR ") + ")"}
			if plan.Back != "" {
				coordAnds = append(coordAnds, likeOnSearchText(plan.Back))
			}
			for _, frag := range plan.ExactFrags {
				coordAnds = append(coordAnds, likeOnSearchText(frag))
			}
			termParts = append(termParts, strings.Join(coordAnds, " AND "))
		}
		where = append(where, "("+strings.Join(termParts, " OR ")+")")
	}
	return where, args
}

// ftsQuote wraps a term as an FTS5 phrase literal (double quotes; any
// embedded double quote doubled).
func ftsQuote(term string) string {
	return `"` + strings.ReplaceAll(term, `"`, `""`) + `"`
}
