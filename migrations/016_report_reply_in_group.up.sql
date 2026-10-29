-- A reporter who never started the bot can't get a private message, so the
-- reply goes to the report's group instead.
-- reports.reply_to_message_id: the message the group reply answers (the
-- user's message with the reported link, else the reporter's own message);
-- NULL for reports filed before this column.
-- report_replies.sent_in_group: the reply was delivered in the report's group,
-- not in the reporter's private chat.
ALTER TABLE reports ADD COLUMN IF NOT EXISTS reply_to_message_id BIGINT;
ALTER TABLE report_replies ADD COLUMN IF NOT EXISTS sent_in_group BOOLEAN NOT NULL DEFAULT false;
