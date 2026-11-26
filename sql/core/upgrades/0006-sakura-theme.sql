-- Adds the sakura family (sakura-light / sakura-dark) to the
-- users.theme_preference enum, placed after washi.
--
-- The column is only widened, so no row is rewritten. A column already in the
-- new shape is left alone, so the file can be run again.

SET NAMES utf8mb4;

DROP PROCEDURE IF EXISTS core_upgrade_0006;

DELIMITER //

CREATE PROCEDURE core_upgrade_0006()
BEGIN
  DECLARE v_new VARCHAR(512) DEFAULT
    'enum(''glass-light'',''glass-dark'',''modern-light'',''modern-dark'',''washi-light'',''washi-dark'',''sakura-light'',''sakura-dark'',''classic-light'',''classic-dark'',''nothing-light'',''nothing-dark'',''system'')';

  IF EXISTS (
    SELECT 1 FROM information_schema.COLUMNS
    WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'users'
      AND COLUMN_NAME = 'theme_preference'
      AND COLUMN_TYPE <> v_new
  ) THEN
    ALTER TABLE users MODIFY COLUMN theme_preference
      ENUM('glass-light','glass-dark','modern-light','modern-dark',
           'washi-light','washi-dark','sakura-light','sakura-dark',
           'classic-light','classic-dark','nothing-light','nothing-dark','system')
      NOT NULL DEFAULT 'system' COMMENT 'UI theme preference';
  END IF;
END//

DELIMITER ;

CALL core_upgrade_0006();

DROP PROCEDURE core_upgrade_0006;
