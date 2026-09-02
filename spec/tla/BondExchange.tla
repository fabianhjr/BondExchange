--------------------------- MODULE BondExchange ---------------------------
EXTENDS BondExchangeActions

(***************************************************************************)
(* Top-level specification for buying one existing sale offer by its ID.   *)
(***************************************************************************)

Init ==
  /\ saleOffers \in SUBSET SaleOffer
  /\ saleOffers # {}
  /\ UniqueSaleOfferIds

Next ==
  \E offerId \in SaleOfferIds : Buy(offerId)

Spec == Init /\ [][Next]_vars

=============================================================================
