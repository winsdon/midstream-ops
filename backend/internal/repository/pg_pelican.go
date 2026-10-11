package repository

import "context"

// PelicanVisibleGroups 客户嵌入页可见的普通分组（排除专属分组）。
func (p *PG) PelicanVisibleGroups(ctx context.Context) ([]int64, error) {
	rows, err := p.pool.Query(ctx, `SELECT id FROM groups WHERE deleted_at IS NULL AND status='active' AND COALESCE(is_exclusive,false)=false`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]int64, 0)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
