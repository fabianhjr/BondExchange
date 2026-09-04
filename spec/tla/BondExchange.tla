--------------------------- MODULE BondExchange ---------------------------
EXTENDS BondExchangeActions

(***************************************************************************)
(* Top-level specification for publishing and buying sale offers.          *)
(***************************************************************************)

Init ==
  /\ saleOffers \in SUBSET SaleOffer
  /\ UniqueSaleOfferIds
  /\ purchases = {}
  /\ operationResults = {}

Next ==
  \/ \E buyer \in Users :
       \E client \in Clients :
         \E key \in IdempotencyKeys :
           \E requestDigest \in RequestDigests :
             \E offerId \in SaleOfferIds :
               Buy(buyer, client, key, requestDigest, offerId)
  \/ \E seller \in Users :
       \E client \in Clients :
         \E key \in IdempotencyKeys :
           \E requestDigest \in RequestDigests :
             \E bond \in Bonds :
               \E offerId \in SaleOfferIds :
                 \E price \in Prices :
                   \E currency \in CurrencyCodes :
                     CreateSaleOffer(seller, client, key, requestDigest, bond, offerId, price, currency)
  \/ \E result \in operationResults :
       RetryCompletedOperation(
         result.principal,
         result.client,
         result.operation,
         result.key,
         result.requestDigest)

Spec == Init /\ [][Next]_vars

=============================================================================
