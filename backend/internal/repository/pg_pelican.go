package repository

import "context"

// ListPelicanKeys is deliberately restricted to the configured test user.
func (p *PG) ListPelicanKeys(ctx context.Context, userID string) ([]PGUserKey, error) {
	keys, err := p.ListUserKeys(ctx, userID)
	if err != nil {
		return nil, err
	}
	rows, err := p.pool.Query(ctx, `SELECT k.id FROM api_keys k JOIN users u ON u.id=k.user_id
 WHERE u.id=$1 AND u.deleted_at IS NULL AND u.status='active'
 AND k.deleted_at IS NULL AND k.status='active'
 AND (NULLIF(to_jsonb(k)->>'expires_at','') IS NULL OR (to_jsonb(k)->>'expires_at')::timestamptz>now())`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	valid := map[int64]bool{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		valid[id] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]PGUserKey, 0)
	for _, k := range keys {
		if valid[k.ID] && k.GroupID > 0 && k.Key != "" {
			out = append(out, k)
		}
	}
	return out, nil
}
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
