-- Disable the enforcement of foreign-keys constraints
PRAGMA foreign_keys = off;
-- Create "new_devices" table
CREATE TABLE `new_devices` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `installation_id` text NOT NULL, `login_id` text NOT NULL, `platform` text NOT NULL, `push_token` text NULL, `device_token` text NULL, `app_id` text NULL, `app_version` text NULL, `build_number` text NULL, `os_version` text NULL, `device_model` text NULL, `locale` text NULL, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL);
-- Copy rows from old table "devices" to new temporary table "new_devices"
INSERT INTO `new_devices` (`id`, `installation_id`, `login_id`, `platform`, `push_token`, `device_token`, `app_id`, `app_version`, `build_number`, `os_version`, `device_model`, `locale`, `created_at`, `updated_at`) SELECT `id`, `installation_id`, `login_id`, `platform`, `push_token`, `device_token`, `app_id`, `app_version`, `build_number`, `os_version`, `device_model`, `locale`, `created_at`, `updated_at` FROM `devices`;
-- Drop "devices" table after copying rows
DROP TABLE `devices`;
-- Rename temporary table "new_devices" to "devices"
ALTER TABLE `new_devices` RENAME TO `devices`;
-- Create index "devices_installation_id_key" to table: "devices"
CREATE UNIQUE INDEX `devices_installation_id_key` ON `devices` (`installation_id`);
-- Create index "device_login_id" to table: "devices"
CREATE INDEX `device_login_id` ON `devices` (`login_id`);
-- Create index "device_push_token" to table: "devices"
CREATE INDEX `device_push_token` ON `devices` (`push_token`);
-- Enable back the enforcement of foreign-keys constraints
PRAGMA foreign_keys = on;
