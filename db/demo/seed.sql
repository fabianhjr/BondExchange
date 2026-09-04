\set ON_ERROR_STOP on

INSERT INTO bond_exchange.users (id)
VALUES
  ('demo-seller'),
  ('demo-buyer');

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

INSERT INTO bond_exchange.bonds (series)
VALUES
  ('DEMO2026'),
  ('DEMO2027');

INSERT INTO bond_exchange.sale_offers
  (id, seller_id, bond_series, price, currency_code)
VALUES
  ('demo-offer-1', 'demo-seller', 'DEMO2026', 99.7500, 'USD'),
  ('demo-offer-2', 'demo-seller', 'DEMO2026', 100.1250, 'USD'),
  ('demo-offer-3', 'demo-seller', 'DEMO2027', 98.5000, 'USD');
