--------------------------- MODULE BondExchange ---------------------------
EXTENDS BondExchangeProperties

(***************************************************************************)
(* Top-level specification for publishing and buying sale offers.          *)
(*                                                                         *)
(* The market starts empty and every offer, purchase, claim, and result is *)
(* produced by a modeled operation, so every fact has provenance. The      *)
(* initial authorization state is the reviewed bootstrap: one trader role  *)
(* carrying both mutation permissions, granted to every user.              *)
(***************************************************************************)

InitialRolePermissionGrants ==
  {[role |-> TraderRole, permission |-> permission, generation |-> 1] :
     permission \in Permissions}

InitialPrincipalRoleGrants ==
  {[principal |-> user, role |-> TraderRole, generation |-> 1] :
     user \in Users}

Init ==
  /\ publishedOffers           = {}
  /\ purchases                 = {}
  /\ claims                    = {}
  /\ results                   = {}
  /\ inFlightBuys              = {}
  /\ rolePermissionGrants      = InitialRolePermissionGrants
  /\ rolePermissionRevocations = {}
  /\ principalRoleGrants       = InitialPrincipalRoleGrants
  /\ principalRoleRevocations  = {}
  /\ suspensions               = {}
  /\ reinstatements            = {}

PublishNext ==
  \E seller \in Users, client \in Clients, key \in IdempotencyKeys,
     requestDigest \in RequestDigests, bond \in Bonds,
     offerId \in SaleOfferIds, price \in Prices :
       CreateSaleOffer(seller, client, key, requestDigest, bond, offerId, price)

BuyNext ==
  \/ \E buyer \in Users, client \in Clients, key \in IdempotencyKeys,
        requestDigest \in RequestDigests, offerId \in SaleOfferIds :
          ClaimBuy(buyer, client, key, requestDigest, offerId)
  \/ \E pending \in inFlightBuys : CommitBuy(pending)
  \/ \E pending \in inFlightBuys : RejectBuy(pending)

RetryNext ==
  \E scope \in ClaimScopes, requestDigest \in RequestDigests :
    \/ ReplayCompletedOperation(scope, requestDigest)
    \/ RetryInFlightOperation(scope, requestDigest)
    \/ RejectConflictingRetry(scope, requestDigest)

AuthorizationNext ==
  \/ \E role \in Roles, permission \in Permissions, generation \in Generations :
       GrantRolePermission(role, permission, generation)
  \/ \E grant \in rolePermissionGrants : RevokeRolePermission(grant)
  \/ \E principal \in Users, role \in Roles, generation \in Generations :
       GrantPrincipalRole(principal, role, generation)
  \/ \E grant \in principalRoleGrants : RevokePrincipalRole(grant)
  \/ \E principal \in Users, generation \in Generations :
       SuspendPrincipal(principal, generation)
  \/ \E suspension \in suspensions : ReinstatePrincipal(suspension)

MarketplaceNext ==
  \/ PublishNext
  \/ BuyNext
  \/ RetryNext

Next ==
  \/ MarketplaceNext
  \/ AuthorizationNext

Spec == Init /\ [][Next]_vars

(***************************************************************************)
(* Checked instances                                                       *)
(*                                                                         *)
(* Marketplace and authorization behavior are checked as separate finite   *)
(* instances. Their state spaces multiply, and each concern needs          *)
(* different constants to stay falsifiable: contention needs several       *)
(* offers, buyers, and prices, while revocation needs several grant        *)
(* generations. Every action is covered by one of the instances below.     *)
(***************************************************************************)

\* Exclusivity, provenance, append-only history, and idempotent retries
\* under a fixed authorization state.
MarketplaceSpec == Init /\ [][MarketplaceNext]_vars

\* Grants, revocations, suspensions, and reinstatements interleaved with
\* marketplace operations, over a deliberately small market.
AuthorizationSpec == Spec

(***************************************************************************)
(* Weak fairness on buy resolution only. Nothing requires an offer to be   *)
(* published or a buy to be claimed, so liveness here asserts that the     *)
(* service always finishes an operation it has already claimed.            *)
(***************************************************************************)

Fairness == \A pending \in PendingBuy : WF_vars(ResolveBuy(pending))

FairSpec == MarketplaceSpec /\ Fairness

THEOREM MarketplaceSpec => []TypeOK
THEOREM MarketplaceSpec => []AtMostOnePurchasePerOffer
THEOREM MarketplaceSpec => []EveryFactHasASucceededOperation
THEOREM MarketplaceSpec => FactsAreAppendOnly
THEOREM AuthorizationSpec => []EffectivePermissionsMatchAuthorizationFacts
THEOREM AuthorizationSpec => NewClaimsAreAuthorizedWhenClaimed
THEOREM FairSpec => EveryInFlightBuyIsResolved

=============================================================================
