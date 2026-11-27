-- v3：实例级「验过之后怎么落地」的配置。
-- default_role：首次自动登记的分级（guest 只给 key 名 / normal 给真值）。
-- auto_group_ids：首次自动登记时自动加入的用户组（JSON 数组）。
ALTER TABLE providers ADD COLUMN default_role TEXT NOT NULL DEFAULT 'guest';
ALTER TABLE providers ADD COLUMN auto_group_ids TEXT NOT NULL DEFAULT '[]';