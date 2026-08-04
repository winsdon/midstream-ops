package repository

import "context"

// PGUser 线上 sub2api 用户（授信建档下拉用）。
//
// 只取 001_init.sql 保证存在的列：username / wechat / total_recharged 都是后续
// 迁移 ALTER 追加的，目标库若未跑到那一版，SELECT 不存在的列会让整个端点 500。
// email 是 UNIQUE NOT NULL，做人肉识别已经够用。
type PGUser struct {
	ID        int64
	Email     string
	Role      string
	Balance   float64
	Status    string
	CreatedAt string // YYYY-MM-DD，仅供展示参考
}

// ListUsers 返回所有未删除用户（授信建档下拉的数据源）。
//
// 不过滤 role 与 status：授信对象是「谁欠钱」，与角色无关（运营方自己可能就是
// admin 账号在跑量）；而 disabled 用户往往正是要追债的那个（欠钱被停号），
// 过滤掉会直接堵死核心场景。两者都如实返回，由前端标记、由人判断。
func (p *PG) ListUsers(ctx context.Context) ([]PGUser, error) {
	rows, err := p.pool.Query(ctx, `
		SELECT id, COALESCE(email,''), COALESCE(role,''),
		       COALESCE(balance,0), COALESCE(status,''),
		       to_char(created_at, 'YYYY-MM-DD')
		FROM users WHERE deleted_at IS NULL
		ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PGUser
	for rows.Next() {
		var u PGUser
		if err := rows.Scan(&u.ID, &u.Email, &u.Role, &u.Balance, &u.Status, &u.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}
