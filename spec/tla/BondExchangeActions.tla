----------------------- MODULE BondExchangeActions -----------------------
EXTENDS FiniteSets, Naturals, Sequences, TLC

(***************************************************************************)
(* Domain, state, and the sole transition action for the bond marketplace. *)
(***************************************************************************)

CONSTANTS Users, Bonds, SaleOfferIds, Prices, CurrencyCodes

VARIABLES saleOffers, purchases

vars == <<saleOffers, purchases>>

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

SaleOfferIdsOf(offers) == {offer.id : offer \in offers}

OffersWithId(offerId) ==
  {offer \in saleOffers : offer.id = offerId}

PurchasedOfferIdsOf(completedPurchases) ==
  {purchase.offer.id : purchase \in completedPurchases}

TypeOK ==
  /\ saleOffers \subseteq SaleOffer
  /\ purchases \subseteq Purchase

UniqueSaleOfferIds ==
  Cardinality(SaleOfferIdsOf(saleOffers)) = Cardinality(saleOffers)

UniquePurchasedOfferIds ==
  Cardinality(PurchasedOfferIdsOf(purchases)) = Cardinality(purchases)

ActiveAndPurchasedOffersAreDisjoint ==
  (SaleOfferIdsOf(saleOffers) \cap PurchasedOfferIdsOf(purchases)) = {}

Buy(buyer, offerId) ==
  /\ buyer \in Users
  /\ Cardinality(OffersWithId(offerId)) = 1
  /\ \E offer \in OffersWithId(offerId) :
       /\ saleOffers' = saleOffers \ {offer}
       /\ purchases' = purchases \cup {[offer |-> offer, buyer |-> buyer]}

=============================================================================
