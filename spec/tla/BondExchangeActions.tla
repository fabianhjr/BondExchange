----------------------- MODULE BondExchangeActions -----------------------
EXTENDS FiniteSets, Naturals, Sequences, TLC

(***************************************************************************)
(* Domain, state, and transition actions for the bond marketplace.         *)
(***************************************************************************)

CONSTANTS Users, Clients, Bonds, SaleOfferIds, Prices, CurrencyCodes,
          IdempotencyKeys, RequestDigests, AuthorizedBuyers,
          AuthorizedOfferSellers

VARIABLES saleOffers, purchases, operationResults

vars == <<saleOffers, purchases, operationResults>>

BuyOperation == "purchases.buy"
CreateOfferOperation == "offers.create"
Operations == {BuyOperation, CreateOfferOperation}

UppercaseAlphaNumericCharacters == {
  "0", "1", "2", "3", "4", "5", "6", "7", "8", "9",
  "A", "B", "C", "D", "E", "F", "G", "H", "I", "J", "K", "L", "M",
  "N", "O", "P", "Q", "R", "S", "T", "U", "V", "W", "X", "Y", "Z"
}

IsValidBondSeries(series) ==
  /\ series \in STRING
  /\ Len(series) \in 3..40
  /\ \A position \in 1..Len(series) :
       SubSeq(series, position, position) \in UppercaseAlphaNumericCharacters

ASSUME /\ Users # {}
       /\ IsFiniteSet(Users)
       /\ Clients # {}
       /\ IsFiniteSet(Clients)
       /\ Bonds # {}
       /\ IsFiniteSet(Bonds)
       /\ \A bond \in Bonds : IsValidBondSeries(bond)
       /\ SaleOfferIds # {}
       /\ IsFiniteSet(SaleOfferIds)
       /\ Prices # {}
       /\ IsFiniteSet(Prices)
       /\ Prices \subseteq (Nat \ {0})
       /\ CurrencyCodes # {}
       /\ IsFiniteSet(CurrencyCodes)
       /\ CurrencyCodes \subseteq STRING
       /\ IdempotencyKeys # {}
       /\ IsFiniteSet(IdempotencyKeys)
       /\ RequestDigests # {}
       /\ IsFiniteSet(RequestDigests)
       /\ AuthorizedBuyers \subseteq Users
       /\ AuthorizedOfferSellers \subseteq Users

SaleOffer == [
  id       : SaleOfferIds,
  seller   : Users,
  bond     : Bonds,
  price    : Prices,
  currency : CurrencyCodes
]

Purchase == [
  offer : SaleOffer,
  buyer : Users
]

OperationResult == [
  principal     : Users,
  client        : Clients,
  operation     : Operations,
  key           : IdempotencyKeys,
  requestDigest : RequestDigests,
  resource      : SaleOfferIds
]

SaleOfferIdsOf(offers) == {offer.id : offer \in offers}

OffersWithId(offerId) ==
  {offer \in saleOffers : offer.id = offerId}

PurchasedOfferIdsOf(completedPurchases) ==
  {purchase.offer.id : purchase \in completedPurchases}

KnownSaleOfferIds ==
  SaleOfferIdsOf(saleOffers) \cup PurchasedOfferIdsOf(purchases)

TypeOK ==
  /\ saleOffers \subseteq SaleOffer
  /\ purchases \subseteq Purchase
  /\ operationResults \subseteq OperationResult

UniqueSaleOfferIds ==
  Cardinality(SaleOfferIdsOf(saleOffers)) = Cardinality(saleOffers)

UniquePurchasedOfferIds ==
  Cardinality(PurchasedOfferIdsOf(purchases)) = Cardinality(purchases)

ActiveAndPurchasedOffersAreDisjoint ==
  (SaleOfferIdsOf(saleOffers) \cap PurchasedOfferIdsOf(purchases)) = {}

OperationScope(result) ==
  <<result.principal, result.client, result.operation, result.key>>

UniqueOperationScopes ==
  Cardinality({OperationScope(result) : result \in operationResults}) =
    Cardinality(operationResults)

IsAuthorized(principal, client, operation) ==
  /\ client \in Clients
  /\ \/ /\ operation = BuyOperation
         /\ principal \in AuthorizedBuyers
     \/ /\ operation = CreateOfferOperation
         /\ principal \in AuthorizedOfferSellers

OnlyAuthorizedOperationsComplete ==
  \A result \in operationResults :
    IsAuthorized(result.principal, result.client, result.operation)

ScopeIsUnused(principal, client, operation, key) ==
  \A result \in operationResults :
    OperationScope(result) # <<principal, client, operation, key>>

Buy(buyer, client, key, requestDigest, offerId) ==
  /\ buyer \in Users
  /\ IsAuthorized(buyer, client, BuyOperation)
  /\ ScopeIsUnused(buyer, client, BuyOperation, key)
  /\ Cardinality(OffersWithId(offerId)) = 1
  /\ \E offer \in OffersWithId(offerId) :
       /\ saleOffers' = saleOffers \ {offer}
       /\ purchases' = purchases \cup {[offer |-> offer, buyer |-> buyer]}
       /\ operationResults' = operationResults \cup {
            [principal     |-> buyer,
             client        |-> client,
             operation     |-> BuyOperation,
             key           |-> key,
             requestDigest |-> requestDigest,
             resource      |-> offerId]
          }

CreateSaleOffer(seller, client, key, requestDigest, bond, offerId, price, currency) ==
  /\ seller \in Users
  /\ IsAuthorized(seller, client, CreateOfferOperation)
  /\ ScopeIsUnused(seller, client, CreateOfferOperation, key)
  /\ bond \in Bonds
  /\ offerId \in SaleOfferIds
  /\ price \in Prices
  /\ currency \in CurrencyCodes
  /\ offerId \notin KnownSaleOfferIds
  /\ saleOffers' = saleOffers \cup {
       [id       |-> offerId,
        seller   |-> seller,
        bond     |-> bond,
        price    |-> price,
        currency |-> currency]
     }
  /\ UNCHANGED purchases
  /\ operationResults' = operationResults \cup {
       [principal     |-> seller,
        client        |-> client,
        operation     |-> CreateOfferOperation,
        key           |-> key,
        requestDigest |-> requestDigest,
        resource      |-> offerId]
     }

RetryCompletedOperation(principal, client, operation, key, requestDigest) ==
  /\ \E result \in operationResults :
       /\ OperationScope(result) = <<principal, client, operation, key>>
       /\ result.requestDigest = requestDigest
  /\ UNCHANGED vars

=============================================================================
