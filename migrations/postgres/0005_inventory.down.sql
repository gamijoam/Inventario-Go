DELETE FROM role_permissions WHERE permission LIKE 'inventory.%';
DROP TABLE inventory_movements;
DROP TABLE inventory_balances;
DROP FUNCTION inventory_guard();
