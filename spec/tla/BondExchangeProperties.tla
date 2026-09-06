---------------------- MODULE BondExchangeProperties ----------------------
EXTENDS BondExchangeActions

(***************************************************************************)
(* State invariants, action properties, and liveness properties.           *)
(*                                                                         *)
(* Properties are grouped by what they protect: fact shape, marketplace    *)
(* exclusivity, provenance of every fact, append-only history, idempotent  *)
(* operation handling, authorization, and non-vacuity.                     *)
(***************************************************************************)

(***************************************************************************)
(* Fact shape                                                              *)
(***************************************************************************)

ResultShapeIsWellFormed ==
  \A result \in results :
    \/ /\ result.outcome   = Succeeded
       /\ result.resource \in SaleOfferIds
       /\ result.errorCode = NoErrorCode
    \/ /\ result.outcome   = Rejected
       /\ result.resource  = NoResource
       /\ result.errorCode \in SafeErrorCodes

TypeOK ==
  /\ publishedOffers           \subseteq SaleOffer
  /\ purchases                 \subseteq Purchase
  /\ claims                    \subseteq Claim
  /\ results                   \subseteq Result
  /\ inFlightBuys              \subseteq PendingBuy
  /\ rolePermissionGrants      \subseteq RolePermissionGrant
  /\ rolePermissionRevocations \subseteq RolePermissionGrant
  /\ principalRoleGrants       \subseteq PrincipalRoleGrant
  /\ principalRoleRevocations  \subseteq PrincipalRoleGrant
  /\ suspensions               \subseteq Suspension
  /\ reinstatements            \subseteq Suspension
  /\ ResultShapeIsWellFormed

(***************************************************************************)
(* Marketplace exclusivity                                                 *)
(*                                                                         *)
(* AtMostOnePurchasePerOffer is the central safety property. Because a buy *)
(* is claimed before it is resolved, two authorized buyers can hold        *)
(* simultaneous claims against one active offer, so this is a property of  *)
(* the resolution rule rather than a consequence of an atomic action.      *)
(***************************************************************************)

UniqueSaleOfferIds ==
  Cardinality(OfferIdsOf(publishedOffers)) = Cardinality(publishedOffers)

AtMostOnePurchasePerOffer ==
  Cardinality(PurchasedOfferIds) = Cardinality(purchases)

ActiveAndPurchasedOffersAreDisjoint ==
  (ActiveOfferIds \cap PurchasedOfferIds) = {}

EveryPurchasedOfferWasPublished ==
  PurchasedOfferIds \subseteq KnownSaleOfferIds

AllPublishedOffersAreMXN ==
  \A offer \in publishedOffers : offer.currency = MXN

NoSelfPurchases ==
  \A purchase \in purchases :
    \A offer \in publishedOffers :
      purchase.offer = offer.id => purchase.buyer # offer.seller

(***************************************************************************)
(* Provenance                                                              *)
(*                                                                         *)
(* No domain fact exists without a succeeded, authorized, idempotent       *)
(* operation, and no succeeded operation exists without its fact. This is  *)
(* the modeled form of the database rule that a successful operation       *)
(* result must reference an existing source fact.                          *)
(***************************************************************************)

EveryResultHasAClaim ==
  \A result \in results : result.claim \in claims

ResultsReferenceKnownResources ==
  \A result \in results :
    result.outcome = Succeeded => result.resource \in KnownSaleOfferIds

EveryFactHasASucceededOperation ==
  /\ OfferIdsOf(publishedOffers) =
       {result.resource : result \in SucceededResults(CreateOfferOperation)}
  /\ PurchasedOfferIds =
       {result.resource : result \in SucceededResults(BuyOperation)}
  /\ Cardinality(SucceededResults(CreateOfferOperation)) = Cardinality(publishedOffers)
  /\ Cardinality(SucceededResults(BuyOperation)) = Cardinality(purchases)

OfferSellersMatchOperationPrincipals ==
  \A offer \in publishedOffers :
    \E result \in SucceededResults(CreateOfferOperation) :
      /\ result.resource = offer.id
      /\ result.claim.scope.principal = offer.seller

PurchaseBuyersMatchOperationPrincipals ==
  \A purchase \in purchases :
    \E result \in SucceededResults(BuyOperation) :
      /\ result.resource = purchase.offer
      /\ result.claim.scope.principal = purchase.buyer

(***************************************************************************)
(* Idempotent operation handling                                           *)
(***************************************************************************)

UniqueClaimScopes ==
  Cardinality(ClaimScopes) = Cardinality(claims)

AtMostOneResultPerClaim ==
  Cardinality(ResolvedClaims) = Cardinality(results)

InFlightBuysAreClaimedAndUnresolved ==
  \A pending \in inFlightBuys :
    /\ pending.claim \in claims
    /\ ResultsForClaim(pending.claim) = {}

(***************************************************************************)
(* Authorization                                                           *)
(*                                                                         *)
(* EffectivePermissionsMatchAuthorizationFacts restates the effective      *)
(* permission view as a predicate over raw facts. The two formulations are *)
(* written independently, so a defect in either is observable.             *)
(***************************************************************************)

HasUnrevokedGrantChain(principal, permission) ==
  \E principalRole \in principalRoleGrants :
    /\ principalRole.principal = principal
    /\ principalRole \notin principalRoleRevocations
    /\ \E rolePermission \in rolePermissionGrants :
         /\ rolePermission.role = principalRole.role
         /\ rolePermission.permission = permission
         /\ rolePermission \notin rolePermissionRevocations

IsSuspended(principal) ==
  \E suspension \in suspensions :
    /\ suspension.principal = principal
    /\ suspension \notin reinstatements

EffectivePermissionsMatchAuthorizationFacts ==
  \A principal \in Users :
    \A permission \in Permissions :
      (permission \in EffectivePermissions(principal)) <=>
        (HasUnrevokedGrantChain(principal, permission) /\ ~IsSuspended(principal))

SuspendedPrincipalsHaveNoPermissions ==
  \A principal \in Users :
    IsSuspended(principal) => EffectivePermissions(principal) = {}

RevocationsReferenceGrants ==
  /\ rolePermissionRevocations \subseteq rolePermissionGrants
  /\ principalRoleRevocations  \subseteq principalRoleGrants
  /\ reinstatements            \subseteq suspensions

(***************************************************************************)
(* Non-vacuity                                                             *)
(*                                                                         *)
(* These invariants fail if a guard becomes so strong that an intended     *)
(* transition is unreachable, which would silently weaken every property   *)
(* above.                                                                  *)
(***************************************************************************)

BuyIsClaimableForAnActiveOffer ==
  \A offer \in ActiveOffers :
    \A buyer \in Users :
      \A client \in Clients :
        \A key \in IdempotencyKeys :
          \A requestDigest \in RequestDigests :
            ( /\ IsAuthorized(buyer, client, BuyOperation)
              /\ ScopeIsUnused(buyer, client, BuyOperation, key) )
              => ENABLED ClaimBuy(buyer, client, key, requestDigest, offer.id)

RetriesAreTotal ==
  \A claim \in claims :
    \A requestDigest \in RequestDigests :
      \/ ENABLED ReplayCompletedOperation(claim.scope, requestDigest)
      \/ ENABLED RetryInFlightOperation(claim.scope, requestDigest)
      \/ ENABLED RejectConflictingRetry(claim.scope, requestDigest)

(***************************************************************************)
(* Append-only history                                                     *)
(*                                                                         *)
(* Every domain fact set only grows, and a published offer's terms never   *)
(* change. Corrections require new facts. These are the properties that a  *)
(* future cancellation, expiry, or settlement action must not break.       *)
(***************************************************************************)

FactsAreAppendOnly ==
  [][ /\ publishedOffers \subseteq publishedOffers'
      /\ purchases       \subseteq purchases'
      /\ claims          \subseteq claims'
      /\ results         \subseteq results' ]_vars

AuthorizationFactsAreAppendOnly ==
  [][ /\ rolePermissionGrants      \subseteq rolePermissionGrants'
      /\ rolePermissionRevocations \subseteq rolePermissionRevocations'
      /\ principalRoleGrants       \subseteq principalRoleGrants'
      /\ principalRoleRevocations  \subseteq principalRoleRevocations'
      /\ suspensions               \subseteq suspensions'
      /\ reinstatements            \subseteq reinstatements' ]_vars

OfferTermsAreImmutable ==
  [][ \A offer \in publishedOffers :
        \A published \in publishedOffers' :
          published.id = offer.id => published = offer ]_vars

PurchasedOffersStayPurchased ==
  [][ PurchasedOfferIds \subseteq {purchase.offer : purchase \in purchases'} ]_vars

SaleOfferIdsAreNeverReused ==
  [][ KnownSaleOfferIds \subseteq OfferIdsOf(publishedOffers') ]_vars

ClaimDigestsAreImmutable ==
  [][ \A claim \in claims :
        \A recorded \in claims' :
          recorded.scope = claim.scope => recorded.requestDigest = claim.requestDigest ]_vars

(***************************************************************************)
(* Authorization is evaluated when an operation is claimed                 *)
(*                                                                         *)
(* The unprimed effective permissions are the ones in force before the     *)
(* step, so this requires a revocation to take effect on the very next     *)
(* claim rather than at some later point.                                  *)
(***************************************************************************)

NewClaimsAreAuthorizedWhenClaimed ==
  [][ \A claim \in claims' \ claims :
        RequiredPermission(claim.scope.operation)
          \in EffectivePermissions(claim.scope.principal) ]_vars

(***************************************************************************)
(* Liveness                                                                *)
(*                                                                         *)
(* A claimed buy is never left in flight: it is always resolved into a     *)
(* committed purchase or a recorded rejection.                             *)
(***************************************************************************)

EveryInFlightBuyIsResolved ==
  \A pending \in PendingBuy :
    (pending \in inFlightBuys) ~> (pending \notin inFlightBuys)

EveryClaimIsEventuallyResolved ==
  \A claim \in Claim :
    (claim \in claims) ~> (ResultsForClaim(claim) # {})

=============================================================================
