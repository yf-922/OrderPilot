package common

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strconv"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

// GetMySQLConnWithRetry tolerates dependency startup races in containers.
func GetMySQLConnWithRetry(ctx context.Context) (*sql.DB, error) {
	delay := 250 * time.Millisecond
	for {
		db, err := GetMySQLConn()
		if err == nil {
			return db, nil
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("mysql unavailable before startup deadline: %w", err)
		case <-time.After(delay):
			if delay < 4*time.Second {
				delay *= 2
			}
		}
	}
}

const (
	// 下面这组默认值用于本地开发环境。
	// 如果没有通过环境变量覆盖配置，就会按这些默认值连接 MySQL。
	defaultMySQLHost     = "127.0.0.1"
	defaultMySQLPort     = 3306
	defaultMySQLUser     = "root"
	defaultMySQLPassword = "root"
	defaultMySQLDatabase = "product"
	defaultMySQLCharset  = "utf8mb4"
)

// MySQLConfig 描述数据库连接所需的核心参数。
type MySQLConfig struct {
	// Host 是 MySQL 服务地址，例如 127.0.0.1 或 localhost。
	Host string
	// Port 是 MySQL 端口，默认一般为 3306。
	Port int
	// User 是数据库用户名。
	User string
	// Password 是数据库密码。
	Password string
	// Database 是要连接的数据库名称。
	Database string
	// Charset 是字符集，常见配置为 utf8mb4。
	Charset string
}

// NewMySQLConfig 读取数据库配置。
// 读取顺序是先准备默认值，再尝试用环境变量覆盖。
func NewMySQLConfig() MySQLConfig {
	port := defaultMySQLPort
	if value := os.Getenv("MYSQL_PORT"); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil {
			port = parsed
		}
	}

	config := MySQLConfig{
		Host:     envOrDefault("MYSQL_HOST", defaultMySQLHost),
		Port:     port,
		User:     envOrDefault("MYSQL_USER", defaultMySQLUser),
		Password: envOrDefault("MYSQL_PASSWORD", defaultMySQLPassword),
		Database: envOrDefault("MYSQL_DATABASE", defaultMySQLDatabase),
		Charset:  envOrDefault("MYSQL_CHARSET", defaultMySQLCharset),
	}

	return config
}

// DSN 将配置转换成 sql.Open 需要的连接字符串。
func (c MySQLConfig) DSN() string {
	return fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?charset=%s&parseTime=true&loc=Local",
		c.User,
		c.Password,
		c.Host,
		c.Port,
		c.Database,
		c.Charset,
	)
}

// GetMySQLConn 是外部最常用的数据库连接入口。
// 它会按默认规则读取配置并创建连接。
func GetMySQLConn() (*sql.DB, error) {
	return GetMySQLConnByConfig(NewMySQLConfig())
}

// GetMySQLConnByConfig 支持外部传入明确配置来创建数据库连接。
func GetMySQLConnByConfig(config MySQLConfig) (*sql.DB, error) {
	db, err := sql.Open("mysql", config.DSN())
	if err != nil {
		return nil, err
	}

	// *sql.DB 本质上是连接池管理器，这里配置连接池参数。
	db.SetMaxOpenConns(20)
	db.SetMaxIdleConns(10)
	db.SetConnMaxLifetime(time.Hour)

	// Ping 主动验证数据库是否可连通，避免把连接错误拖到业务查询阶段。
	if err = db.Ping(); err != nil {
		_ = db.Close()
		return nil, err
	}

	return db, nil
}

// QueryRowsToMap 执行查询并将结果统一转换成 []map[string]any。
// 适合在 repository 层做通用查询结果处理。
func QueryRowsToMap(db *sql.DB, query string, args ...any) ([]map[string]any, error) {
	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return RowsToMap(rows)
}

// RowsToMap 将原始查询结果 rows 转成更容易消费的 map 结构。
// 转换后可以通过列名直接取值，例如 item["productName"]。
func RowsToMap(rows *sql.Rows) ([]map[string]any, error) {
	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}

	result := make([]map[string]any, 0)

	for rows.Next() {
		values := make([]any, len(columns))
		scanArgs := make([]any, len(columns))
		for i := range values {
			scanArgs[i] = &values[i]
		}

		if err = rows.Scan(scanArgs...); err != nil {
			return nil, err
		}

		rowMap := make(map[string]any, len(columns))
		for i, column := range columns {
			rowMap[column] = normalizeDBValue(values[i])
		}
		result = append(result, rowMap)
	}

	if err = rows.Err(); err != nil {
		return nil, err
	}

	return result, nil
}

// normalizeDBValue 统一处理数据库返回值的类型差异。
// 常见场景是将 []byte 转成 string，避免上层继续做额外转换。
func normalizeDBValue(value any) any {
	switch v := value.(type) {
	case []byte:
		return string(v)
	default:
		return v
	}
}

// envOrDefault 是读取环境变量的小工具。
// 如果环境变量不存在，就返回调用方提供的默认值。
func envOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
