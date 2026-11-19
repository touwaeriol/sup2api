-- The accounts.credentials and accounts.read host permissions are removed:
-- a plugin receives the accounts of the account types it declares without a
-- grant (CONTRACTS §124). A plugin upgrade prunes the grants its new manifest
-- no longer asks for, but not revoked rows, nor the grants of a plugin not
-- upgraded or of a staged version; they would show in the review as unknown
-- permissions. Plain deletes: a rerun changes nothing.
DELETE FROM plugin_permission_grants WHERE permission IN ('accounts.credentials', 'accounts.read');
DELETE FROM plugin_staged_grants WHERE permission IN ('accounts.credentials', 'accounts.read');
