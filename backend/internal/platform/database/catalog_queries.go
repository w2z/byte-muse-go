package database

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"

	"bytemuse/backend/internal/domain"
	"bytemuse/backend/internal/ports"
)

// LegacyRankImportResult summarizes rank types and ordered entries read from the legacy cache table.
type LegacyRankImportResult struct {
	RankTypes int
	Entries   int
}

// ImportLegacyRanks imports the latest cache snapshot for every legacy rank type.
func ImportLegacyRanks(ctx context.Context, target Store, legacyPath string) (LegacyRankImportResult, error) {
	source, err := sql.Open("sqlite", "file:"+legacyPath+"?mode=ro")
	if err != nil {
		return LegacyRankImportResult{}, fmt.Errorf("open legacy database: %w", err)
	}
	defer source.Close()
	rows, err := source.QueryContext(ctx, "SELECT key, content, create_time FROM cache WHERE namespace = 'rank' AND content IS NOT NULL AND trim(content) <> '' ORDER BY key ASC, create_time DESC, id DESC")
	if err != nil {
		return LegacyRankImportResult{}, fmt.Errorf("read legacy ranks: %w", err)
	}
	defer rows.Close()
	type snapshot struct{ content, created string }
	snapshots := make(map[string]snapshot)
	order := make([]string, 0)
	for rows.Next() {
		var key, content string
		var created sql.NullString
		if err := rows.Scan(&key, &content, &created); err != nil {
			return LegacyRankImportResult{}, err
		}
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if _, exists := snapshots[key]; exists {
			continue
		}
		snapshots[key] = snapshot{content: content, created: created.String}
		order = append(order, key)
	}
	if err := rows.Err(); err != nil {
		return LegacyRankImportResult{}, err
	}
	result := LegacyRankImportResult{}
	tx, err := target.SQLDB().BeginTx(ctx, nil)
	if err != nil {
		return LegacyRankImportResult{}, fmt.Errorf("begin legacy rank import: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	for _, key := range order {
		snapshot := snapshots[key]
		if _, err := tx.ExecContext(ctx, "DELETE FROM rank_entries WHERE rank_type = ?", key); err != nil {
			return LegacyRankImportResult{}, fmt.Errorf("replace rank %q: %w", key, err)
		}
		seen := make(map[string]struct{})
		position := 0
		for _, rawCode := range strings.Split(snapshot.content, ",") {
			code := strings.TrimSpace(rawCode)
			if code == "" {
				continue
			}
			normalized := strings.ToUpper(code)
			if _, duplicate := seen[normalized]; duplicate {
				continue
			}
			seen[normalized] = struct{}{}
			position++
			if _, err := tx.ExecContext(ctx, "INSERT INTO rank_entries (rank_type, position, code, source_created_at) VALUES (?, ?, ?, ?)", key, position, code, nullIfEmpty(snapshot.created)); err != nil {
				return LegacyRankImportResult{}, fmt.Errorf("insert rank %q position %d: %w", key, position, err)
			}
			result.Entries++
		}
		result.RankTypes++
	}
	if err := tx.Commit(); err != nil {
		return LegacyRankImportResult{}, fmt.Errorf("commit legacy rank import: %w", err)
	}
	committed = true
	return result, nil
}

func nullIfEmpty(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

// CatalogQueryRepository queries migrated catalog search and rank views without changing catalog state.
type CatalogQueryRepository struct {
	db      *sql.DB
	dialect Dialect
}

// NewCatalogQueryRepository creates catalog read queries backed by a migrated store.
func NewCatalogQueryRepository(db *sql.DB, dialect Dialect) *CatalogQueryRepository {
	return &CatalogQueryRepository{db: db, dialect: dialect}
}

// Search returns catalog rows matching code, title, or translated title. Empty query returns the full catalog.
func (r *CatalogQueryRepository) Search(ctx context.Context, query string, limit, offset int) (domain.MediaPage, error) {
	limit, offset = normalizePagination(limit, offset)
	term := strings.TrimSpace(query)
	where := ""
	args := make([]any, 0, 3)
	if term != "" {
		pattern := "%" + strings.ToUpper(term) + "%"
		where = fmt.Sprintf(" WHERE UPPER(m.code) LIKE %s OR UPPER(m.title) LIKE %s OR UPPER(COALESCE(m.translated_title, '')) LIKE %s", placeholder(r.dialect, 1), placeholder(r.dialect, 2), placeholder(r.dialect, 3))
		args = append(args, pattern, pattern, pattern)
	}
	var total int
	if err := r.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM media m"+where, args...).Scan(&total); err != nil {
		return domain.MediaPage{}, err
	}
	selectArgs := append(append([]any{}, args...), limit, offset)
	rows, err := r.db.QueryContext(ctx, fmt.Sprintf("SELECT %s FROM media m %s%s ORDER BY m.updated_at DESC, m.id ASC LIMIT %s OFFSET %s", mediaProjectionColumns("m"), mediaProjectionJoins(), where, placeholder(r.dialect, len(selectArgs)-1), placeholder(r.dialect, len(selectArgs))), selectArgs...)
	if err != nil {
		return domain.MediaPage{}, err
	}
	defer rows.Close()
	items, err := scanMediaProjectionRows(ctx, r.db, r.dialect, rows)
	if err != nil {
		return domain.MediaPage{}, err
	}
	return domain.MediaPage{Items: items, Total: total}, nil
}

// Rank returns only catalog rows resolved from one ordered rank cache snapshot.
func (r *CatalogQueryRepository) Rank(ctx context.Context, rankType string, limit, offset int, filters ports.MediaListQuery) (domain.MediaPage, error) {
	limit, offset = normalizePagination(limit, offset)
	where, args := mediaListWhere(r.dialect, filters)
	if where == "" {
		where = " WHERE 1=1"
	}
	args = append(args, rankType)
	where += " AND r.rank_type = " + placeholder(r.dialect, len(args))
	var total int
	if err := r.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM rank_entries r JOIN media m ON UPPER(m.code)=UPPER(r.code)"+where, args...).Scan(&total); err != nil {
		return domain.MediaPage{}, err
	}
	args = append(args, limit, offset)
	rows, err := r.db.QueryContext(ctx, fmt.Sprintf("SELECT %s FROM rank_entries r JOIN media m ON UPPER(m.code)=UPPER(r.code) %s%s ORDER BY r.position ASC LIMIT %s OFFSET %s", mediaProjectionColumns("m"), mediaProjectionJoins(), where, placeholder(r.dialect, len(args)-1), placeholder(r.dialect, len(args))), args...)
	if err != nil {
		return domain.MediaPage{}, err
	}
	defer rows.Close()
	items, err := scanMediaProjectionRows(ctx, r.db, r.dialect, rows)
	if err != nil {
		return domain.MediaPage{}, err
	}
	return domain.MediaPage{Items: items, Total: total}, nil
}

// ReleaseToday returns only rows whose persisted release date matches the requested calendar date.
func (r *CatalogQueryRepository) ReleaseToday(ctx context.Context, releaseDate string, limit, offset int, filters ports.MediaListQuery) (domain.MediaPage, error) {
	limit, offset = normalizePagination(limit, offset)
	where, args := mediaListWhere(r.dialect, filters)
	if where == "" {
		where = " WHERE 1=1"
	}
	args = append(args, releaseDate)
	where += " AND m.release_date = " + placeholder(r.dialect, len(args))
	var total int
	if err := r.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM media m"+where, args...).Scan(&total); err != nil {
		return domain.MediaPage{}, err
	}
	args = append(args, limit, offset)
	rows, err := r.db.QueryContext(ctx, fmt.Sprintf("SELECT %s FROM media m %s%s ORDER BY m.code ASC LIMIT %s OFFSET %s", mediaProjectionColumns("m"), mediaProjectionJoins(), where, placeholder(r.dialect, len(args)-1), placeholder(r.dialect, len(args))), args...)
	if err != nil {
		return domain.MediaPage{}, err
	}
	defer rows.Close()
	items, err := scanMediaProjectionRows(ctx, r.db, r.dialect, rows)
	if err != nil {
		return domain.MediaPage{}, err
	}
	return domain.MediaPage{Items: items, Total: total}, nil
}

type recommendationMetadata struct {
	id        string
	release   string
	genres    []string
	casts     []string
	series    []string
	publisher []string
	score     int
}

// Recommend derives a preference profile exclusively from persisted subscriptions/completions,
// then ranks matching catalog rows using the shared subscription and type filters in the requested release window.
func (r *CatalogQueryRepository) Recommend(ctx context.Context, startDate, endDate string, limit, offset int, filters ports.MediaListQuery) (domain.MediaPage, error) {
	limit, offset = normalizePagination(limit, offset)
	profile, err := r.recommendationProfile(ctx)
	if err != nil {
		return domain.MediaPage{}, err
	}
	if profile.empty() {
		return domain.MediaPage{Items: []domain.Media{}}, nil
	}
	candidates, err := r.recommendationCandidates(ctx, startDate, endDate, filters)
	if err != nil {
		return domain.MediaPage{}, err
	}
	for index := range candidates {
		candidates[index].score = profile.score(candidates[index])
	}
	filtered := candidates[:0]
	for _, candidate := range candidates {
		if candidate.score > 0 {
			filtered = append(filtered, candidate)
		}
	}
	sort.SliceStable(filtered, func(left, right int) bool {
		if filtered[left].score != filtered[right].score {
			return filtered[left].score > filtered[right].score
		}
		if filtered[left].release != filtered[right].release {
			return filtered[left].release > filtered[right].release
		}
		return filtered[left].id < filtered[right].id
	})
	total := len(filtered)
	if offset >= total {
		return domain.MediaPage{Items: []domain.Media{}, Total: total}, nil
	}
	end := min(offset+limit, total)
	selected := filtered[offset:end]
	ids := make([]string, 0, len(selected))
	for _, candidate := range selected {
		ids = append(ids, candidate.id)
	}
	items, err := r.loadMediaProjectionBatch(ctx, ids)
	if err != nil {
		return domain.MediaPage{}, err
	}
	return domain.MediaPage{Items: items, Total: total}, nil
}

// loadMediaProjectionBatch loads complete media projections in one query and restores the requested ID order.
func (r *CatalogQueryRepository) loadMediaProjectionBatch(ctx context.Context, ids []string) ([]domain.Media, error) {
	if len(ids) == 0 {
		return []domain.Media{}, nil
	}
	placeholdersSQL := placeholders(r.dialect, len(ids), 1)
	args := make([]any, len(ids))
	for index, id := range ids {
		args[index] = id
	}
	rows, err := r.db.QueryContext(ctx, fmt.Sprintf("SELECT %s FROM media m %s WHERE m.id IN (%s)", mediaProjectionColumns("m"), mediaProjectionJoins(), placeholdersSQL), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	loaded, err := scanMediaProjectionRows(ctx, r.db, r.dialect, rows)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]domain.Media, len(loaded))
	for _, item := range loaded {
		byID[item.ID] = item
	}
	ordered := make([]domain.Media, 0, len(ids))
	for _, id := range ids {
		if item, ok := byID[id]; ok {
			ordered = append(ordered, item)
		}
	}
	return ordered, nil
}

type recommendationProfile struct {
	genres, casts, series, publishers map[string]int
}

func (r *CatalogQueryRepository) recommendationProfile(ctx context.Context) (recommendationProfile, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT lm.genres, lm.casts, lm.series, lm.publisher
FROM legacy_media_metadata lm
JOIN media m ON m.id = lm.media_id
WHERE lm.legacy_status IN ('SUBSCRIBE', 'COMPLETE')
   OR m.subscription_status = 'active'
   OR m.library_status = 'present'`)
	if err != nil {
		return recommendationProfile{}, err
	}
	defer rows.Close()
	genreCounts, castCounts := map[string]int{}, map[string]int{}
	seriesCounts, publisherCounts := map[string]int{}, map[string]int{}
	for rows.Next() {
		var genres, casts, series, publisher sql.NullString
		if err := rows.Scan(&genres, &casts, &series, &publisher); err != nil {
			return recommendationProfile{}, err
		}
		countRecommendationValues(genreCounts, splitRecommendationValues(genres.String))
		countRecommendationValues(castCounts, splitRecommendationValues(casts.String))
		countRecommendationValues(seriesCounts, splitRecommendationValues(series.String))
		countRecommendationValues(publisherCounts, splitRecommendationValues(publisher.String))
	}
	if err := rows.Err(); err != nil {
		return recommendationProfile{}, err
	}
	return recommendationProfile{
		genres: topRecommendationScores(genreCounts), casts: topRecommendationScores(castCounts),
		series: topRecommendationScores(seriesCounts), publishers: topRecommendationScores(publisherCounts),
	}, nil
}

func (r *CatalogQueryRepository) recommendationCandidates(ctx context.Context, startDate, endDate string, filters ports.MediaListQuery) ([]recommendationMetadata, error) {
	where, args := mediaListWhere(r.dialect, filters)
	if where == "" {
		where = " WHERE 1=1"
	}
	args = append(args, startDate, endDate)
	where += fmt.Sprintf(" AND m.release_date BETWEEN %s AND %s", placeholder(r.dialect, len(args)-1), placeholder(r.dialect, len(args)))
	rows, err := r.db.QueryContext(ctx, "SELECT m.id,m.release_date,lm.genres,lm.casts,lm.series,lm.publisher FROM media m JOIN legacy_media_metadata lm ON lm.media_id=m.id"+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]recommendationMetadata, 0)
	for rows.Next() {
		var item recommendationMetadata
		var release, genres, casts, series, publisher sql.NullString
		if err := rows.Scan(&item.id, &release, &genres, &casts, &series, &publisher); err != nil {
			return nil, err
		}
		item.release = release.String
		item.genres = splitRecommendationValues(genres.String)
		item.casts = splitRecommendationValues(casts.String)
		item.series = splitRecommendationValues(series.String)
		item.publisher = splitRecommendationValues(publisher.String)
		items = append(items, item)
	}
	return items, rows.Err()
}

func (p recommendationProfile) empty() bool {
	return len(p.genres) == 0 && len(p.casts) == 0 && len(p.series) == 0 && len(p.publishers) == 0
}

func (p recommendationProfile) score(item recommendationMetadata) int {
	score := recommendationValueScore(item.genres, p.genres)
	if len(item.casts) <= 3 {
		score += 5 * recommendationValueScore(item.casts, p.casts)
	}
	score += 2 * recommendationValueScore(item.series, p.series)
	score += 2 * recommendationValueScore(item.publisher, p.publishers)
	return score
}

func splitRecommendationValues(raw string) []string {
	parts := strings.Split(raw, ",")
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		if value := strings.TrimSpace(part); value != "" {
			values = append(values, value)
		}
	}
	return values
}

func countRecommendationValues(counts map[string]int, values []string) {
	for _, value := range values {
		counts[value]++
	}
}

func topRecommendationScores(counts map[string]int) map[string]int {
	type rankedValue struct {
		value string
		count int
	}
	ranked := make([]rankedValue, 0, len(counts))
	for value, count := range counts {
		ranked = append(ranked, rankedValue{value: value, count: count})
	}
	sort.Slice(ranked, func(left, right int) bool {
		if ranked[left].count != ranked[right].count {
			return ranked[left].count > ranked[right].count
		}
		return ranked[left].value < ranked[right].value
	})
	scores := make(map[string]int, min(10, len(ranked)))
	for index := 0; index < min(10, len(ranked)); index++ {
		scores[ranked[index].value] = 10 - index
	}
	return scores
}

func recommendationValueScore(values []string, scores map[string]int) int {
	total := 0
	for _, value := range values {
		total += scores[value]
	}
	return total
}

func prefixedMediaColumns(alias string) string {
	parts := strings.Split(mediaColumns(), ", ")
	for i := range parts {
		parts[i] = alias + "." + parts[i]
	}
	return strings.Join(parts, ", ")
}
