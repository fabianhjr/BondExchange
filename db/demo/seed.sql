\set ON_ERROR_STOP on

INSERT INTO bond_exchange.users (uuid_id)
VALUES
  ('01991a20-0000-7000-8000-000000000001'),
  ('01991a20-0000-7000-8000-000000000002'),
  ('01991a20-0000-7000-8000-000000000003');

INSERT INTO bond_exchange.principals (uuid_id, issuer, subject, client_class)
VALUES
  ('01991a20-0000-7000-8000-000000000001', 'https://demo-issuer.invalid', 'demo-seller', 'human'),
  ('01991a20-0000-7000-8000-000000000002', 'https://demo-issuer.invalid', 'demo-buyer', 'human'),
  ('01991a20-0000-7000-8000-000000000003', 'https://demo-issuer.invalid', 'demo-rate-limited', 'automated');

INSERT INTO bond_exchange.principal_role_grants
  (principal_uuid, role_uuid, reason)
SELECT principal.uuid_id, role.uuid_id, seed.reason
FROM (VALUES
  ('01991a20-0000-7000-8000-000000000001'::uuid, 'trader', 'Disposable demo access.'),
  ('01991a20-0000-7000-8000-000000000002'::uuid, 'trader', 'Disposable demo access.'),
  ('01991a20-0000-7000-8000-000000000002'::uuid, 'operator', 'Disposable demo health access.'),
  ('01991a20-0000-7000-8000-000000000003'::uuid, 'trader', 'Disposable rate-limit test access.')
) AS seed(principal_uuid, role_code, reason)
JOIN bond_exchange.principals AS principal ON principal.uuid_id = seed.principal_uuid
JOIN bond_exchange.roles AS role ON role.code = seed.role_code;

INSERT INTO bond_exchange.bonds (series, uuid_id)
VALUES
  ('DEMO2026', '01991a20-0000-7000-8000-000000000010'),
  ('DEMO2027', '01991a20-0000-7000-8000-000000000011');

INSERT INTO bond_exchange.sale_offers
  (uuid_id, seller_uuid, bond_uuid, price, currency_code)
SELECT seed.uuid_id, '01991a20-0000-7000-8000-000000000001', bond.uuid_id, seed.price, 'MXN'
FROM (VALUES
  ('01991a20-0000-7000-8000-000000000101'::uuid, 'DEMO2026', 99.7500),
  ('01991a20-0000-7000-8000-000000000102'::uuid, 'DEMO2026', 100.1250),
  ('01991a20-0000-7000-8000-000000000103'::uuid, 'DEMO2027', 98.5000)
) AS seed(uuid_id, bond_series, price)
JOIN bond_exchange.bonds AS bond ON bond.series = seed.bond_series;

INSERT INTO bond_exchange.sale_offer_canonical_terms
  (sale_offer_uuid, price, currency_code)
SELECT uuid_id, price, 'MXN'
FROM bond_exchange.sale_offers;

INSERT INTO bond_exchange.sale_offer_submissions
  (sale_offer_uuid, submitted_price, submitted_currency_code)
SELECT uuid_id, price, 'MXN'
FROM bond_exchange.sale_offers;

-- The disposable demo remains deterministic and does not contact Banxico.
-- Production never seeds rates: it obtains and persists SF43718 through SIE.
INSERT INTO bond_exchange.sie_exchange_rate_imports
  (uuid_id, request_kind, series_ids, response_body, response_sha256)
VALUES (
  '01991a20-0000-7000-8000-000000000020',
  'latest',
  ARRAY['SF43718'],
  '{"bmx":{"series":[{"idSerie":"SF43718","datos":[{"fecha":"04/09/2026","dato":"17.0000"}]}]}}',
  decode(repeat('00', 32), 'hex')
);

INSERT INTO bond_exchange.sie_exchange_rate_observations
  (uuid_id, import_uuid, series_id, base_currency, quote_currency, observed_on, value)
VALUES (
  '01991a20-0000-7000-8000-000000000021',
  '01991a20-0000-7000-8000-000000000020',
  'SF43718',
  'USD',
  'MXN',
  current_date,
  17.0000
);

INSERT INTO bond_exchange.sie_exchange_rate_fetch_coordination
  (work_key, series_id, base_currency, quote_currency, request_kind, completed_at, fresh_until)
VALUES (
  'latest:SF43718:USD:MXN',
  'SF43718',
  'USD',
  'MXN',
  'latest',
  transaction_timestamp(),
  transaction_timestamp() + interval '1 day'
);
