package store

import "gorm.io/gorm"

// FindPage reads page (from 1) of size rows of q into T, with the count of
// all of them. q is one chain with its conditions, joins, select and order;
// FindPage derives the count and the page from it as two statements of a
// fresh session, so neither changes q nor sees the other's clauses. The
// count is GORM's: it drops the ORDER BY and counts the rows of q (count(*)
// for a column list), so the joins must not multiply them.
func FindPage[T any](q *gorm.DB, page, size int) ([]T, int64, error) {
	s := q.Session(&gorm.Session{})
	var total int64
	if err := s.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	items := []T{}
	err := s.Limit(size).Offset((page - 1) * size).Scan(&items).Error
	return items, total, err
}
