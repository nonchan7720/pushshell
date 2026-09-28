-- Create "device_logins" table
CREATE TABLE `device_logins` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `login_id` text NOT NULL, `created_at` datetime NOT NULL, `device_logins` integer NOT NULL, CONSTRAINT `device_logins_devices_logins` FOREIGN KEY (`device_logins`) REFERENCES `devices` (`id`) ON DELETE CASCADE);
-- Create index "devicelogin_login_id_device_logins" to table: "device_logins"
CREATE UNIQUE INDEX `devicelogin_login_id_device_logins` ON `device_logins` (`login_id`, `device_logins`);
-- Create index "devicelogin_login_id" to table: "device_logins"
CREATE INDEX `devicelogin_login_id` ON `device_logins` (`login_id`);
