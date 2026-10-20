-- 9GAG media was recorded under the platform 'unknown': the stats mapping
-- had no 9GAG case. Relabel those rows by their URL.
UPDATE media_stats
SET platform = '9gag'
WHERE (platform = 'unknown') AND (url ~* '^https?://([a-z0-9-]+\.)*9gag\.com(/|$)');
