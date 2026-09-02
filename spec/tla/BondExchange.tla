--------------------------- MODULE BondExchange ---------------------------
EXTENDS BondExchangeActions

(***************************************************************************)
(* Top-level specification for one user buying one existing sale offer.    *)
(***************************************************************************)

Init ==
  /\ saleOffers \in SUBSET SaleOffer
  /\ saleOffers # {}
  /\ UniqueSaleOfferIds
  /\ purchases = {}

Next ==
  \E buyer \in Users :
    \E offerId \in SaleOfferIds : Buy(buyer, offerId)

Spec == Init /\ [][Next]_vars

=============================================================================
