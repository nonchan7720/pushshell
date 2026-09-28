-- Create "devices" table
CREATE TABLE `devices` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `installation_id` text NOT NULL, `login_id` text NOT NULL, `platform` text NOT NULL, `push_token` text NOT NULL, `device_token` text NULL, `app_id` text NULL, `app_version` text NULL, `build_number` text NULL, `os_version` text NULL, `device_model` text NULL, `locale` text NULL, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL);
-- Create index "devices_installation_id_key" to table: "devices"
CREATE UNIQUE INDEX `devices_installation_id_key` ON `devices` (`installation_id`);
-- Create index "device_login_id" to table: "devices"
CREATE INDEX `device_login_id` ON `devices` (`login_id`);
-- Create index "device_push_token" to table: "devices"
CREATE INDEX `device_push_token` ON `devices` (`push_token`);
