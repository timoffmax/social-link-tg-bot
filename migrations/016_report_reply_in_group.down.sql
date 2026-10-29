ALTER TABLE report_replies DROP COLUMN IF EXISTS sent_in_group;
ALTER TABLE reports DROP COLUMN IF EXISTS reply_to_message_id;
