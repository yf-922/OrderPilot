# 数据库迁移说明

`db/schema.sql` 适用于全新环境。应用启动时会执行 `common.EnsureSchema`，对已有的 `order` 表做向前兼容检查：

- 缺少 `quantity` 时新增字段，默认值为 `1`；
- 缺少 `idempotencyKey` 时新增可空字段，并用 `legacy-{ID}` 回填；
- 将幂等字段收紧为非空，并补充唯一索引。

迁移只增加字段和索引，不会删除业务数据。生产环境仍建议在发布流程中使用独立的 schema migration 工具（例如 Atlas、Goose 或 Flyway），并在执行前备份数据库。应用启动迁移适合本地开发、演示和单实例部署。
