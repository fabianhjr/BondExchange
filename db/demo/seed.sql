\set ON_ERROR_STOP on

INSERT INTO bond_exchange.users (id)
VALUES
  ('demo-seller'),
  ('demo-buyer');

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
