-- Revierte 0010. Los precios publicados se pierden: la reversión solo es
-- segura antes de usarlos.
DELETE FROM role_permissions WHERE permission IN ('pricing.price.read', 'pricing.list.manage', 'pricing.price.propose', 'pricing.price.approve');

DROP TABLE price_proposals;
DROP TABLE price_revisions;
DROP TABLE price_margins;
DROP TABLE price_lists;
DROP FUNCTION pricing_guard();
