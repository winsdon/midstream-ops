package repository

import (
	"context"
	"database/sql"
	"strconv"
	"strings"
)

// rebind 把 `?` 占位符按序换成 PostgreSQL 的 $1..$N。
//
// 【为什么不解析字符串字面量】全库约 500 处 `?` 已逐一核验：SQL 字符串字面量
// 内部无一含 `?`。要正确跳过字面量得处理单引号、双引号标识符、$$ 美元引用、
// `--` 与 `/* */` 注释四类上下文，是几十行带状态机的新代码——用来防一个当前
// 不存在、且一旦出现就会被立刻抓到的问题：占位符错位会导致参数个数不匹配，
// PG 直接报错，不是静默错误。失败模式响亮，才是不付防御成本的理由。
// rebind_test.go 里有一个测试扫描全包源码守住这个前提。
//
// 【安全边界】本函数只接受仓储层的常量 SQL 模板。绝不能用它处理拼接了用户
// 输入的字符串——那本身就是注入，与本函数无关。
func rebind(query string) string {
	if !strings.ContainsRune(query, '?') {
		return query
	}
	var b strings.Builder
	b.Grow(len(query) + 16)
	n := 0
	for i := 0; i < len(query); i++ {
		if query[i] != '?' {
			b.WriteByte(query[i])
			continue
		}
		n++
		b.WriteByte('$')
		b.WriteString(strconv.Itoa(n))
	}
	return b.String()
}

// DB 是 *sql.DB 的薄壳：转发全部方法，只在进入驱动前把 `?` 换成 `$N`。
//
// 【为什么不直接改写 SQL 文本】仓储层有约 500 个占位符，机械替换的 diff 无法
// review，且 4 处运行时动态拼接的 WHERE 需要手工维护递增计数器，漏改一处就是
// 运行时才暴露的参数错位。薄壳让所有 SQL 文本一字不改，迁移 diff 收敛到每个
// repo 的字段类型那一行。
type DB struct{ raw *sql.DB }

// Raw 暴露底层 *sql.DB（迁移器与连接池配置用）。
func (d *DB) Raw() *sql.DB { return d.raw }

func (d *DB) ExecContext(ctx context.Context, q string, a ...any) (sql.Result, error) {
	return d.raw.ExecContext(ctx, rebind(q), a...)
}

func (d *DB) QueryContext(ctx context.Context, q string, a ...any) (*sql.Rows, error) {
	return d.raw.QueryContext(ctx, rebind(q), a...)
}

func (d *DB) QueryRowContext(ctx context.Context, q string, a ...any) *sql.Row {
	return d.raw.QueryRowContext(ctx, rebind(q), a...)
}

// PrepareContext 预编译语句。返回原生 *sql.Stmt 而非包壳：
// (*sql.Stmt).ExecContext 的签名里没有 SQL 字符串——SQL 在 Prepare 时就固定了，
// rebind 在这一层做完就够。这是薄壳能收敛得这么小的关键。
func (d *DB) PrepareContext(ctx context.Context, q string) (*sql.Stmt, error) {
	return d.raw.PrepareContext(ctx, rebind(q))
}

// Exec / Query / QueryRow 无 ctx 版本，供测试里的裸 SQL 使用。
func (d *DB) Exec(q string, a ...any) (sql.Result, error) { return d.raw.Exec(rebind(q), a...) }
func (d *DB) Query(q string, a ...any) (*sql.Rows, error) { return d.raw.Query(rebind(q), a...) }
func (d *DB) QueryRow(q string, a ...any) *sql.Row        { return d.raw.QueryRow(rebind(q), a...) }

// BeginTx 开启事务。
//
// 必须返回自定义 *Tx 而非 *sql.Tx：否则 11 处事务里的全部 tx.ExecContext /
// tx.QueryRowContext 都会带着 `?` 直达 PG。这是本设计唯一的强制约束。
func (d *DB) BeginTx(ctx context.Context, opts *sql.TxOptions) (*Tx, error) {
	tx, err := d.raw.BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	return &Tx{raw: tx}, nil
}

func (d *DB) PingContext(ctx context.Context) error { return d.raw.PingContext(ctx) }
func (d *DB) Close() error                          { return d.raw.Close() }

// Tx 是 *sql.Tx 的薄壳，语义同 DB。
type Tx struct{ raw *sql.Tx }

func (t *Tx) ExecContext(ctx context.Context, q string, a ...any) (sql.Result, error) {
	return t.raw.ExecContext(ctx, rebind(q), a...)
}

func (t *Tx) QueryContext(ctx context.Context, q string, a ...any) (*sql.Rows, error) {
	return t.raw.QueryContext(ctx, rebind(q), a...)
}

func (t *Tx) QueryRowContext(ctx context.Context, q string, a ...any) *sql.Row {
	return t.raw.QueryRowContext(ctx, rebind(q), a...)
}

func (t *Tx) PrepareContext(ctx context.Context, q string) (*sql.Stmt, error) {
	return t.raw.PrepareContext(ctx, rebind(q))
}

func (t *Tx) Exec(q string, a ...any) (sql.Result, error) { return t.raw.Exec(rebind(q), a...) }

func (t *Tx) Commit() error   { return t.raw.Commit() }
func (t *Tx) Rollback() error { return t.raw.Rollback() }
