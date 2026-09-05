\set ON_ERROR_STOP on

INSERT INTO bond_exchange.users (uuid_id)
VALUES
  ('01991a20-0000-7000-8000-000000000001'),
  ('01991a20-0000-7000-8000-000000000002');

INSERT INTO bond_exchange.principals (uuid_id, issuer, subject, client_class)
VALUES
  ('01991a20-0000-7000-8000-000000000001', 'https://demo-issuer.invalid', 'demo-seller', 'human'),
  ('01991a20-0000-7000-8000-000000000002', 'https://demo-issuer.invalid', 'demo-buyer', 'human');

INSERT INTO bond_exchange.principal_role_grants
  (principal_uuid, role_uuid, reason)
SELECT principal.uuid_id, role.uuid_id, seed.reason
FROM (VALUES
  ('01991a20-0000-7000-8000-000000000001'::uuid, 'trader', 'Disposable demo access.'),
  ('01991a20-0000-7000-8000-000000000002'::uuid, 'trader', 'Disposable demo access.'),
  ('01991a20-0000-7000-8000-000000000002'::uuid, 'operator', 'Disposable demo health access.')
) AS seed(principal_uuid, role_code, reason)
JOIN bond_exchange.principals AS principal ON principal.uuid_id = seed.principal_uuid
JOIN bond_exchange.roles AS role ON role.code = seed.role_code;

INSERT INTO bond_exchange.bonds (series, uuid_id)
VALUES
  ('DEMO2026', '01991a20-0000-7000-8000-000000000010'),
  ('DEMO2027', '01991a20-0000-7000-8000-000000000011');

INSERT INTO bond_exchange.sale_offers
  (uuid_id, seller_uuid, bond_uuid, price, currency_code)
SELECT seed.uuid_id, '01991a20-0000-7000-8000-000000000001', bond.uuid_id, seed.price, 'USD'
FROM (VALUES
  ('01991a20-0000-7000-8000-000000000101'::uuid, 'DEMO2026', 99.7500),
  ('01991a20-0000-7000-8000-000000000102'::uuid, 'DEMO2026', 100.1250),
  ('01991a20-0000-7000-8000-000000000103'::uuid, 'DEMO2027', 98.5000)
) AS seed(uuid_id, bond_series, price)
JOIN bond_exchange.bonds AS bond ON bond.series = seed.bond_series;
