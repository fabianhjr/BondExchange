----------------------- MODULE BondExchangeActions -----------------------
EXTENDS FiniteSets, Naturals, Sequences, TLC

(***************************************************************************)
(* Domain, append-only facts, and transition actions for the bond          *)
(* marketplace.                                                            *)
(*                                                                         *)
(* Every domain fact is an append-only set. The active sale book is not a  *)
(* fact: it is derived from published offers and purchases, matching the   *)
(* service's derived active-offer read. The only non-monotonic variable is *)
(* inFlightBuys, which represents binding orders that have been claimed    *)
(* but not yet resolved, so that two orders can contend for one offer.     *)
(***************************************************************************)

CONSTANTS Users, Clients, Bonds, SaleOfferIds, Prices, MXN,
          IdempotencyKeys, RequestDigests, MaxGrantGeneration

VARIABLES
  publishedOffers,            \* every sale offer ever published
  purchases,                  \* every binding order ever committed
  claims,                     \* every claimed idempotent operation scope
  results,                    \* the outcome recorded for a resolved claim
  inFlightBuys,               \* claimed buy operations awaiting resolution
  rolePermissionGrants,
  rolePermissionRevocations,
  principalRoleGrants,
  principalRoleRevocations,
  suspensions,
  reinstatements

exchangeVars  == <<publishedOffers, purchases>>
operationVars == <<claims, results, inFlightBuys>>
authorizationVars ==
  <<rolePermissionGrants, rolePermissionRevocations,
    principalRoleGrants, principalRoleRevocations,
    suspensions, reinstatements>>

vars == <<exchangeVars, operationVars, authorizationVars>>

(***************************************************************************)
(* Operations, permissions, and roles                                      *)
(***************************************************************************)

BuyOperation         == "purchases.buy"
CreateOfferOperation == "offers.create"
Operations           == {BuyOperation, CreateOfferOperation}

BuyPermission         == "purchases.buy"
CreateOfferPermission == "offers.create"
Permissions           == {BuyPermission, CreateOfferPermission}

RequiredPermission(operation) ==
  CASE operation = BuyOperation         -> BuyPermission
    [] operation = CreateOfferOperation -> CreateOfferPermission

TraderRole == "trader"
Roles      == {TraderRole}

Generations == 1..MaxGrantGeneration

(***************************************************************************)
(* Operation outcomes                                                      *)
(***************************************************************************)

Succeeded == "succeeded"
Rejected  == "rejected"
Outcomes  == {Succeeded, Rejected}

OfferUnavailable == "offer_unavailable"
SelfTradeProhibited == "self_trade_prohibited"
SafeErrorCodes   == {OfferUnavailable, SelfTradeProhibited}

NoResource  == "no_resource"
NoErrorCode == "no_error_code"

(***************************************************************************)
(* Bond series validation                                                  *)
(***************************************************************************)

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
       /\ MXN \in STRING
       /\ IdempotencyKeys # {}
       /\ IsFiniteSet(IdempotencyKeys)
       /\ RequestDigests # {}
       /\ IsFiniteSet(RequestDigests)
       /\ MaxGrantGeneration \in Nat \ {0}

(***************************************************************************)
(* Fact shapes                                                             *)
(***************************************************************************)

SaleOffer == [
  id       : SaleOfferIds,
  seller   : Users,
  bond     : Bonds,
  price    : Prices,
  currency : {MXN}
]

Purchase == [
  offer : SaleOfferIds,
  buyer : Users
]

Scope == [
  principal : Users,
  client    : Clients,
  operation : Operations,
  key       : IdempotencyKeys
]

Claim == [
  scope         : Scope,
  requestDigest : RequestDigests
]

Result == [
  claim     : Claim,
  outcome   : Outcomes,
  resource  : SaleOfferIds \cup {NoResource},
  errorCode : SafeErrorCodes \cup {NoErrorCode}
]

PendingBuy == [
  claim : Claim,
  offer : SaleOfferIds
]

RolePermissionGrant == [
  role       : Roles,
  permission : Permissions,
  generation : Generations
]

PrincipalRoleGrant == [
  principal  : Users,
  role       : Roles,
  generation : Generations
]

Suspension == [
  principal  : Users,
  generation : Generations
]

(***************************************************************************)
(* Derived reads                                                           *)
(***************************************************************************)

OfferIdsOf(offers) == {offer.id : offer \in offers}

PurchasedOfferIds == {purchase.offer : purchase \in purchases}

KnownSaleOfferIds == OfferIdsOf(publishedOffers)

\* The active sale book is a view over append-only facts, never a fact.
ActiveOffers == {offer \in publishedOffers : offer.id \notin PurchasedOfferIds}

ActiveOfferIds == OfferIdsOf(ActiveOffers)

TradableOffers(buyer) ==
  {offer \in ActiveOffers : offer.seller # buyer}

TradableOfferIds(buyer) == OfferIdsOf(TradableOffers(buyer))

ClaimScopes == {claim.scope : claim \in claims}

ResolvedClaims == {result.claim : result \in results}

ResultsForClaim(claim) == {result \in results : result.claim = claim}

SucceededResults(operation) ==
  {result \in results :
     /\ result.outcome = Succeeded
     /\ result.claim.scope.operation = operation}

MakeScope(principal, client, operation, key) ==
  [principal |-> principal, client |-> client, operation |-> operation, key |-> key]

ScopeIsUnused(principal, client, operation, key) ==
  MakeScope(principal, client, operation, key) \notin ClaimScopes

(***************************************************************************)
(* Effective authorization                                                 *)
(*                                                                         *)
(* This mirrors the shape of the effective-permission view: join           *)
(* unrevoked principal-role grants to unrevoked role-permission grants,    *)
(* then exclude principals under an unreinstated suspension.               *)
(***************************************************************************)

UnrevokedRolePermissionGrants == rolePermissionGrants \ rolePermissionRevocations
UnrevokedPrincipalRoleGrants  == principalRoleGrants \ principalRoleRevocations
UnreinstatedSuspensions       == suspensions \ reinstatements

EffectivePermissions(principal) ==
  IF \E suspension \in UnreinstatedSuspensions : suspension.principal = principal
  THEN {}
  ELSE LET granted ==
             {rolePermission \in UnrevokedRolePermissionGrants :
                \E principalRole \in UnrevokedPrincipalRoleGrants :
                  /\ principalRole.principal = principal
                  /\ principalRole.role = rolePermission.role}
       IN {rolePermission.permission : rolePermission \in granted}

IsAuthorized(principal, client, operation) ==
  /\ client \in Clients
  /\ RequiredPermission(operation) \in EffectivePermissions(principal)

(***************************************************************************)
(* Marketplace actions                                                     *)
(*                                                                         *)
(* Publishing is atomic: no other request contends for a fresh offer       *)
(* identity. Buying is claimed and then resolved, so that two authorized   *)
(* buyers can both hold a claim against one active offer and the           *)
(* specification must decide which one acquires it.                        *)
(***************************************************************************)

CreateSaleOffer(seller, client, key, requestDigest, bond, offerId, price) ==
  /\ IsAuthorized(seller, client, CreateOfferOperation)
  /\ ScopeIsUnused(seller, client, CreateOfferOperation, key)
  /\ offerId \notin KnownSaleOfferIds
  /\ LET claim == [scope         |-> MakeScope(seller, client, CreateOfferOperation, key),
                   requestDigest |-> requestDigest]
     IN /\ publishedOffers' = publishedOffers \cup {
              [id       |-> offerId,
               seller   |-> seller,
               bond     |-> bond,
               price    |-> price,
               currency |-> MXN]
            }
        /\ claims' = claims \cup {claim}
        /\ results' = results \cup {
              [claim     |-> claim,
               outcome   |-> Succeeded,
               resource  |-> offerId,
               errorCode |-> NoErrorCode]
            }
  /\ UNCHANGED <<purchases, inFlightBuys>>
  /\ UNCHANGED authorizationVars

ClaimBuy(buyer, client, key, requestDigest, offerId) ==
  /\ IsAuthorized(buyer, client, BuyOperation)
  /\ ScopeIsUnused(buyer, client, BuyOperation, key)
  /\ LET claim == [scope         |-> MakeScope(buyer, client, BuyOperation, key),
                   requestDigest |-> requestDigest]
     IN /\ claims' = claims \cup {claim}
        /\ inFlightBuys' = inFlightBuys \cup {[claim |-> claim, offer |-> offerId]}
  /\ UNCHANGED <<publishedOffers, purchases, results>>
  /\ UNCHANGED authorizationVars

CommitBuy(pending) ==
  /\ pending \in inFlightBuys
  /\ pending.offer \in TradableOfferIds(pending.claim.scope.principal)
  /\ purchases' = purchases \cup {
       [offer |-> pending.offer, buyer |-> pending.claim.scope.principal]
     }
  /\ results' = results \cup {
       [claim     |-> pending.claim,
        outcome   |-> Succeeded,
        resource  |-> pending.offer,
        errorCode |-> NoErrorCode]
     }
  /\ inFlightBuys' = inFlightBuys \ {pending}
  /\ UNCHANGED <<publishedOffers, claims>>
  /\ UNCHANGED authorizationVars

RejectBuy(pending) ==
  /\ pending \in inFlightBuys
  /\ pending.offer \notin ActiveOfferIds
  /\ results' = results \cup {
       [claim     |-> pending.claim,
        outcome   |-> Rejected,
        resource  |-> NoResource,
        errorCode |-> OfferUnavailable]
     }
  /\ inFlightBuys' = inFlightBuys \ {pending}
  /\ UNCHANGED <<publishedOffers, purchases, claims>>
  /\ UNCHANGED authorizationVars

RejectSelfBuy(pending) ==
  /\ pending \in inFlightBuys
  /\ pending.offer \in ActiveOfferIds
  /\ pending.offer \notin TradableOfferIds(pending.claim.scope.principal)
  /\ results' = results \cup {
       [claim     |-> pending.claim,
        outcome   |-> Rejected,
        resource  |-> NoResource,
        errorCode |-> SelfTradeProhibited]
     }
  /\ inFlightBuys' = inFlightBuys \ {pending}
  /\ UNCHANGED <<publishedOffers, purchases, claims>>
  /\ UNCHANGED authorizationVars

ResolveBuy(pending) ==
  CommitBuy(pending) \/ RejectBuy(pending) \/ RejectSelfBuy(pending)

(***************************************************************************)
(* Retry actions                                                           *)
(*                                                                         *)
(* Every retry of a claimed scope is handled and leaves all facts          *)
(* unchanged. The three actions are distinct so that TLC coverage shows    *)
(* each path is reachable and RetriesAreTotal can require that no retry is *)
(* stuck.                                                                  *)
(***************************************************************************)

ReplayCompletedOperation(scope, requestDigest) ==
  /\ \E claim \in claims :
       /\ claim.scope = scope
       /\ claim.requestDigest = requestDigest
       /\ ResultsForClaim(claim) # {}
  /\ UNCHANGED vars

RetryInFlightOperation(scope, requestDigest) ==
  /\ \E claim \in claims :
       /\ claim.scope = scope
       /\ claim.requestDigest = requestDigest
       /\ ResultsForClaim(claim) = {}
  /\ UNCHANGED vars

RejectConflictingRetry(scope, requestDigest) ==
  /\ \E claim \in claims :
       /\ claim.scope = scope
       /\ claim.requestDigest # requestDigest
  /\ UNCHANGED vars

(***************************************************************************)
(* Authorization actions                                                   *)
(*                                                                         *)
(* Authorization changes are themselves append-only facts. A revocation    *)
(* names the grant it revokes, and a reinstatement names the suspension it *)
(* lifts, so restoring access requires a new grant generation rather than  *)
(* the retraction of a fact.                                               *)
(*                                                                         *)
(* Granting and suspending are enabled only when the corresponding access  *)
(* state is not already in force. An operator restores access that has     *)
(* been revoked; re-asserting access that already holds is not a modeled   *)
(* administrative decision.                                                *)
(***************************************************************************)

GrantRolePermission(role, permission, generation) ==
  /\ ~\E effective \in UnrevokedRolePermissionGrants :
        /\ effective.role = role
        /\ effective.permission = permission
  /\ LET grant == [role |-> role, permission |-> permission, generation |-> generation]
     IN /\ grant \notin rolePermissionGrants
        /\ rolePermissionGrants' = rolePermissionGrants \cup {grant}
  /\ UNCHANGED <<rolePermissionRevocations, principalRoleGrants,
                 principalRoleRevocations, suspensions, reinstatements>>
  /\ UNCHANGED exchangeVars
  /\ UNCHANGED operationVars

RevokeRolePermission(grant) ==
  /\ grant \in rolePermissionGrants
  /\ grant \notin rolePermissionRevocations
  /\ rolePermissionRevocations' = rolePermissionRevocations \cup {grant}
  /\ UNCHANGED <<rolePermissionGrants, principalRoleGrants,
                 principalRoleRevocations, suspensions, reinstatements>>
  /\ UNCHANGED exchangeVars
  /\ UNCHANGED operationVars

GrantPrincipalRole(principal, role, generation) ==
  /\ ~\E effective \in UnrevokedPrincipalRoleGrants :
        /\ effective.principal = principal
        /\ effective.role = role
  /\ LET grant == [principal |-> principal, role |-> role, generation |-> generation]
     IN /\ grant \notin principalRoleGrants
        /\ principalRoleGrants' = principalRoleGrants \cup {grant}
  /\ UNCHANGED <<rolePermissionGrants, rolePermissionRevocations,
                 principalRoleRevocations, suspensions, reinstatements>>
  /\ UNCHANGED exchangeVars
  /\ UNCHANGED operationVars

RevokePrincipalRole(grant) ==
  /\ grant \in principalRoleGrants
  /\ grant \notin principalRoleRevocations
  /\ principalRoleRevocations' = principalRoleRevocations \cup {grant}
  /\ UNCHANGED <<rolePermissionGrants, rolePermissionRevocations,
                 principalRoleGrants, suspensions, reinstatements>>
  /\ UNCHANGED exchangeVars
  /\ UNCHANGED operationVars

SuspendPrincipal(principal, generation) ==
  /\ ~\E effective \in UnreinstatedSuspensions : effective.principal = principal
  /\ LET suspension == [principal |-> principal, generation |-> generation]
     IN /\ suspension \notin suspensions
        /\ suspensions' = suspensions \cup {suspension}
  /\ UNCHANGED <<rolePermissionGrants, rolePermissionRevocations,
                 principalRoleGrants, principalRoleRevocations, reinstatements>>
  /\ UNCHANGED exchangeVars
  /\ UNCHANGED operationVars

ReinstatePrincipal(suspension) ==
  /\ suspension \in suspensions
  /\ suspension \notin reinstatements
  /\ reinstatements' = reinstatements \cup {suspension}
  /\ UNCHANGED <<rolePermissionGrants, rolePermissionRevocations,
                 principalRoleGrants, principalRoleRevocations, suspensions>>
  /\ UNCHANGED exchangeVars
  /\ UNCHANGED operationVars

=============================================================================
