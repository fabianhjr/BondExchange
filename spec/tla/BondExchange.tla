--------------------------- MODULE BondExchange ---------------------------
EXTENDS BondExchangeActions

(***************************************************************************)
(* Top-level specification for publishing and buying sale offers.          *)
(***************************************************************************)

Init ==
  /\ saleOffers \in SUBSET SaleOffer
  /\ UniqueSaleOfferIds
  /\ purchases = {}

Next ==
  \/ \E buyer \in Users :
       \E offerId \in SaleOfferIds : Buy(buyer, offerId)
  \/ \E seller \in Users :
       \E bond \in Bonds :
         \E offerId \in SaleOfferIds :
           \E price \in Prices :
             \E currency \in CurrencyCodes :
               CreateSaleOffer(seller, bond, offerId, price, currency)

Spec == Init /\ [][Next]_vars

=============================================================================
