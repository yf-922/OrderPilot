package common

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// EnsureSchema applies the small, backwards-compatible migrations required by
// the current API. The Docker bootstrap creates the latest schema, while this
// function keeps an existing developer database upgradeable without a manual
// destructive reset.
func EnsureSchema(ctx context.Context, db *sql.DB) error {
	if db == nil {
		return fmt.Errorf("database connection is nil")
	}
	var database string
	if err := db.QueryRowContext(ctx, "SELECT DATABASE()").Scan(&database); err != nil {
		return err
	}
	if database == "" {
		return fmt.Errorf("no database selected")
	}
	for _, table := range []string{"User", "product", "order"} {
		if ok, err := hasTable(ctx, db, database, table); err != nil {
			return err
		} else if !ok {
			return fmt.Errorf("required table `%s` does not exist", table)
		}
	}
	if length, err := columnLength(ctx, db, database, "User", "HashPassword"); err != nil {
		return err
	} else if length < 255 {
		if _, err := db.ExecContext(ctx, "ALTER TABLE `User` MODIFY COLUMN `HashPassword` VARCHAR(255) NOT NULL"); err != nil {
			return fmt.Errorf("expand User.HashPassword: %w", err)
		}
	}

	if ok, err := hasColumn(ctx, db, database, "order", "quantity"); err != nil {
		return err
	} else if !ok {
		if _, err := db.ExecContext(ctx, "ALTER TABLE `order` ADD COLUMN `quantity` BIGINT NOT NULL DEFAULT 1 AFTER `productID`"); err != nil {
			return fmt.Errorf("add order.quantity: %w", err)
		}
	}
	if ok, err := hasColumn(ctx, db, database, "order", "idempotencyKey"); err != nil {
		return err
	} else if !ok {
		if _, err := db.ExecContext(ctx, "ALTER TABLE `order` ADD COLUMN `idempotencyKey` VARCHAR(128) NULL AFTER `quantity`"); err != nil {
			return fmt.Errorf("add order.idempotencyKey: %w", err)
		}
	}
	if _, err := db.ExecContext(ctx, "UPDATE `order` SET `idempotencyKey`=CONCAT('legacy-', `ID`) WHERE `idempotencyKey` IS NULL OR `idempotencyKey`='' "); err != nil {
		return fmt.Errorf("backfill order.idempotencyKey: %w", err)
	}
	if _, err := db.ExecContext(ctx, "ALTER TABLE `order` MODIFY COLUMN `idempotencyKey` VARCHAR(128) NOT NULL"); err != nil {
		return fmt.Errorf("enforce order.idempotencyKey: %w", err)
	}
	if ok, err := hasIndex(ctx, db, database, "order", "uk_order_idempotency"); err != nil {
		return err
	} else if !ok {
		if _, err := db.ExecContext(ctx, "ALTER TABLE `order` ADD UNIQUE INDEX `uk_order_idempotency` (`idempotencyKey`)"); err != nil {
			return fmt.Errorf("add order idempotency index: %w", err)
		}
	}
	return nil
}

func hasColumn(ctx context.Context, db *sql.DB, database, table, column string) (bool, error) {
	var count int
	err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.columns WHERE table_schema=? AND table_name=? AND column_name=?`, database, table, column).Scan(&count)
	return count > 0, err
}

func columnLength(ctx context.Context, db *sql.DB, database, table, column string) (int64, error) {
	var length sql.NullInt64
	err := db.QueryRowContext(ctx, `SELECT character_maximum_length FROM information_schema.columns WHERE table_schema=? AND table_name=? AND column_name=?`, database, table, column).Scan(&length)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("required column `%s.%s` does not exist", table, column)
	}
	if err != nil {
		return 0, err
	}
	if !length.Valid {
		return 0, fmt.Errorf("column `%s.%s` has no character length", table, column)
	}
	return length.Int64, nil
}

func hasTable(ctx context.Context, db *sql.DB, database, table string) (bool, error) {
	var count int
	err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=? AND table_name=?`, database, table).Scan(&count)
	return count > 0, err
}

func hasIndex(ctx context.Context, db *sql.DB, database, table, index string) (bool, error) {
	var count int
	err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.statistics WHERE table_schema=? AND table_name=? AND index_name=?`, database, table, index).Scan(&count)
	return count > 0, err
}
