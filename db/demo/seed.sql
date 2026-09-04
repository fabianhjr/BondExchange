\set ON_ERROR_STOP on

INSERT INTO bond_exchange.users (id, uuid_id)
VALUES
  ('demo-seller', '01991a20-0000-7000-8000-000000000001'),
  ('demo-buyer', '01991a20-0000-7000-8000-000000000002');

INSERT INTO bond_exchange.principals (id, issuer, subject, client_class)
VALUES
  ('demo-seller', 'https://demo-issuer.invalid', 'demo-seller', 'human'),
  ('demo-buyer', 'https://demo-issuer.invalid', 'demo-buyer', 'human');

INSERT INTO bond_exchange.principal_role_grants
  (id, principal_id, role_id, reason)
VALUES
  ('demo-seller-trader', 'demo-seller', 'trader', 'Disposable demo access.'),
  ('demo-buyer-trader', 'demo-buyer', 'trader', 'Disposable demo access.'),
  ('demo-buyer-operator', 'demo-buyer', 'operator', 'Disposable demo health access.');

INSERT INTO bond_exchange.bonds (series, uuid_id)
VALUES
  ('DEMO2026', '01991a20-0000-7000-8000-000000000010'),
  ('DEMO2027', '01991a20-0000-7000-8000-000000000011');

INSERT INTO bond_exchange.sale_offers
  (id, uuid_id, seller_id, bond_series, price, currency_code)
VALUES
  ('demo-offer-1', '01991a20-0000-7000-8000-000000000101', 'demo-seller', 'DEMO2026', 99.7500, 'USD'),
  ('demo-offer-2', '01991a20-0000-7000-8000-000000000102', 'demo-seller', 'DEMO2026', 100.1250, 'USD'),
  ('demo-offer-3', '01991a20-0000-7000-8000-000000000103', 'demo-seller', 'DEMO2027', 98.5000, 'USD');
