-- Create "device_logins" table
CREATE TABLE `device_logins` (`id` bigint NOT NULL AUTO_INCREMENT, `login_id` varchar(256) NOT NULL, `created_at` timestamp NOT NULL, `device_logins` bigint NOT NULL, PRIMARY KEY (`id`), INDEX `devicelogin_login_id` (`login_id`), UNIQUE INDEX `devicelogin_login_id_device_logins` (`login_id`, `device_logins`), CONSTRAINT `device_logins_devices_logins` FOREIGN KEY (`device_logins`) REFERENCES `devices` (`id`) ON DELETE CASCADE);
-- Copy existing devices.login_id rows into device_logins before the column is dropped
INSERT INTO `device_logins` (`login_id`, `created_at`, `device_logins`) SELECT `login_id`, `created_at`, `id` FROM `devices`;
-- Modify "devices" table
ALTER TABLE `devices` DROP INDEX `device_login_id`, DROP COLUMN `login_id`;
