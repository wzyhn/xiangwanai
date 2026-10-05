-- 830_xiangwan_scheduled_publications.down.sql
DROP TABLE IF EXISTS xiangwan_scheduled_publications;
DROP FUNCTION IF EXISTS xiangwan_reject_scheduled_publication_mutation();
DROP FUNCTION IF EXISTS xiangwan_reject_scheduled_publication_truncate();
