-- An apikey account of volcengine (official Ark) has one fixed path layout:
-- api_prefix and video_api_prefix are settings of the relay type only, not
-- in the apikey type's settingsFields, not read for it (prefixesOf) and
-- refused as unknown keys when its credentials are edited (CONTRACTS §126).
-- Accounts saved before the form refused them still carry the keys, so every
-- credential edit fails and the console cannot remove them. Drop the two
-- keys from those accounts; relay accounts and other plugins keep theirs.
-- Only rows holding a key change: a rerun changes nothing.
UPDATE accounts
SET settings = settings - 'api_prefix' - 'video_api_prefix'
WHERE plugin_key = 'volcengine' AND type = 'apikey'
  AND settings ?| array['api_prefix', 'video_api_prefix'];
