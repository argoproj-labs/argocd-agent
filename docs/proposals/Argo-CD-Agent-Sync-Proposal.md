# Investigating challenging cases Argo CD Agent must handle

Written by Jonathan West (@jgwest), originally proposed in September 2024. 
* Lightly edited for GitHub in March 2026.

# Introduction

I have identified some cases that [in September 2024, when this was originally written] we are not currently handling within the Argo CD agent code. Primarily these cases relate to network connectivity loss, unexpected OS process restart, and modifications to Application .spec while the OS process is not running.

In addition to giving examples of these cases, I’ve also proposed an algorithm (and a set of behaviour changes) that should solve these issues.

The proposed algorithm is only as complex as the problem itself. Distributing computing is a naturally hard problem. (Of course, if you think the proposed changes are too complex, feel free to propose a less complex solution that solves the same problem while avoiding the failure cases described below.)

**Note**: Significant parts of this document only refer to synchronization of Argo CD Application .spec between managed-mode agent and principal. The same logic also applies in the reverse direction, re: autonomous mode/principal, and also likely to AppProject synchronization.

**The challenging cases can be broken down into these categories:**

* Maintaining sync after Agent restart  
* Maintaining sync after Principal restart  
* Maintaining sync after both restart at the same time  
* Maintaining sync while events occur while agent is disconnected from principal (not receiving events)  
* Maintaining sync while events occur (on principal cluster) while the principal is not watching for cluster events (for example due to controller being offline)  
* Maintaining sync while events occur (on agent cluster) while the agent  is not watching for cluster events (for example due to controller being offline)

# Challenging cases Argo Agent must handle

An architecture needs to be reliable in 100% of cases, not 90% of cases. These are some of that  challenging 10%.

**A) No mechanism to re-send list of Applications in the case where managed-agent namespace, or its contents, are deleted/modified while the managed agent is not running**

1. Principal creates an Application A, sends that event to managed agent, managed agent creates Application A in namespace NS.  
2. Managed-agent process is killed.  
3. Managed-agent namespace NS (from above step) is deleted (or, instead, just the Applications within it are deleted).  
4. Managed-agent namespace NS is (re-)created.  
5. Managed-agent process is (re-)started.  
6. Managed-agent connects to the principal.  
7. At this point, a managed agent needs some mechanism to re-acquire the list of Applications. BUT, currently, no such mechanism exists: the managed agent is not re-sent the list of Applications it needs to re-create.

Currently no such solution exists for this in the code base AFAIK. The  ‘request-basic-entity-list’ and ‘request-update’ messages described below are the solution proposed for this, here.

**B) Applications are orphaned on the managed-agent if they are deleted on the principal control plane while the principal is not running**

1. Principal and managed agent are running and connected  
2. Application 'a' is created, principal sees that and sends message to managed-agent  
3. Managed agent receive message, and creates Argo CD Application 'a'  
4. *(Principal process dies: it OOMs, the Node it is on reboots, Pod is deleted, etc)*  
5. User deletes Application 'a', with principal process is offline  
6. Principal process starts again  
7. Principal starts watching again, BUT principal has no mechanism to be informed of resources that no longer exist.   
   1. Since the principal was not running, there was no watch active. Thus no events are sent for delete.  
   2. Kubernetes API will only ever tell you about resources that are deleted while Watch API is active.  
8. Since the Application 'a' was deleted in step 5, the principal DOES NOT inform the agent that it was deleted.  
   1. Application 'a' is thus orphaned on managed agent, and will not be deleted (at least until the agent restarts, and a 'liveness-check' is sent on agent startup)

The goal of the **‘request-entity-resync’** message/behaviour is to guard against this race condition. In the principal/managed agent case, the **‘request-entity-resync’** is sent from the principal to the managed agent. It informs the managed agent that an OS process restart occurred. The managed agent will then need to re-verify its list of Applications, via **‘request-updates’s.**

Alternative options considered besides **‘request-entity-resync’**:

* Add a custom finalizer to all Applications  
  * We could add a custom finalizer to all Argo CD Applications, and only remove that finalizer once we had communicated the deletion to the agent.   
  * HOWEVER, this solution has the problem that it’s trivial for users to remove the finalizer, and when they do the Application is orphaned on the agent side.  
* Persistent volume with sqlite DB that keeps track of the last reconciled state of applications, and on startup can detect what is missing.   
  * This was the Red Hat AppStudio Managed GitOps Service solution (with postgres)

**C) Application is orphaned on managed-agent, when delete events occur on principal during a period where principal and managed agent have lost connection**

1. Managed agent connects to principal  
2. User creates Argo CD Application 'loan-app' in a principal Namespace  
3. Principal sends the managed agent a message, informing the agent that a new Application 'loan-app' was created.  
4. Agent creates Argo CD Application 'loan-app' in Argo CD namespace.  
5. *(Connection is lost between managed agent and principal, but both are still running)*  
6. User deletes Argo CD Application 'loan-app' in a principal Namespace  
   1. (or user updates app, or user creates a new app, the problem is the same)  
7. Principal attempts to send a delete message to the agent, but no agent is connected, so the message is discarded.  
8. *(Agent successfully reconnects to principal)*  
9. (BUT, since Agent missed the delete message, it still has Application 'loan-app' in the namespace, and it will never be deleted)

The proposed solution to this is that we switch to never dropping messages that we are unable to send. Thus, when the agent reconnects (after the final step) it will receive the delete msg then.

**D) Managed agent may intermittently miss some events due to network instability, because Principal thinks a message has been sent to the agent, but the message is actually lost in transit**

1. Managed agent connects to the principal.  
2. Principal sends the managed agent a message, informing the agent that a new Application 'loan-app' was created.  
3. Principal thinks the message was sent, and removes it from the outbound message queue  
4. *(BUT, agent never receives the message, due to network instability)*  
5. Managed agent (re-)connects to principal.  
6. *(BUT, at this point, the managed agent is not informed of the message, because the principal thinks it was sent, and removed it from the queue. Agent is never informed of the created application.)*

The proposed solution to this problem is that messages should only be removed from the outbound queue when they are acknowledged by the recipient as BOTH received AND processed.

**E) Unexpected behaviour can occur when an Application is deleted, then created with the same name, while managed agent is disconnected from principal**

1. Managed agent connects to principal  
2. User creates Argo CD Application 'loan-app' in a principal Namespace  
3. Principal sends the managed agent a message, informing the agent that a new Application 'loan-app' was created.  
4. Agent creates Argo CD Application 'loan-app' in Argo CD namespace.  
5. *(Connection is lost between managed agent and principal, but both are still running)*  
6. User deletes Argo CD Application ‘loan-app’, and replaces it with a different application with the same name.  
7. Managed agent reconnects to principal  
8. Managed agent is informed of loan-app **.spec** change (from old app to new app), and modifies the existing ‘loan-app’ to the new .spec.   
   1. HOWEVER, this is not what the user expected. The user expected that:  
      1. Old Argo CD Application would be deleted  
      2. New app would be created with the new .spec content  
   2. Instead what they got is  
      1. Old app still exists, but its .spec was modified. This difference can matter in more complex cases. For example: what if the managed agent sends the .status field from the old Argo CD Application, even though the .spec field corresponds to principal from new Application.

The proposed solution to this is to use both application name and application UID within events, and update logic to detect cases where they differ.

**F) Both principal and agent processes are restarted at the same time.**

* In this case, the inbox/outbox for both processes is lost.  
* The algorithm could be simpler in some ways if we assumed that at no point would both the managed agent and principal restart at the same time.  
* However, I’m not sure if this is true.

# Proposed Algorithm

## Algorithm goals and constraints

**Goal of the algorithm is:**

* To ensure that the agent and the principal are kept in sync at all times during normal conditions.  
* All APIs are stateless/async. There is no 'state' where a principal/agent is blocked, waiting for a specific message response.  
  * Or said another way, all events/messages are fire and forget. All the information that is required to perform the 'next action' of an event/message is self-contained within the message itself.  
  * Or, said another way, the pattern is always:  
    * goroutine receives a message, performs some local action, sends one or more response messages, then returns. Any replies will be handled by another goroutine.  
  * If we wanted to switch to a stateful, synchronous handshake, we could produce a slightly more efficient algorithm, but it would be (much?) more complex to implement. I'm trying to avoid that here.  
* APIs should aim to send many small messages, rather than one big message. Sending large GRPC messages (for example, on startup, sending full state from ‘source of truth’) may not scale to a large number of applications, and is vulnerable to issues with slow/unreliable connections.  
* To ensure that applications are brought into sync as quickly as possible after network instability, OS process restart, etc.  
  * Or, said another way, agent and principal synchronization is eventual consistency.   
  * The goal is that the ‘eventual’ in ‘eventual consistency’ be as short as possible.  
* To ensure that if an agent process restarts, or principal process restarts, or both, applications are quickly brought into sync.  
* To ensure that if agent/principal temporarily lose connection, that no events are missed between them, and applications are quickly brought into sync on reconnect.  
* To ensure that no applications are ever left orphaned.   
  * An application is orphaned if it exists on a managed agent, but does not exist on a control plane. (Or on control plane, but not autonomous agent)  
* Avoid race conditions related to message ordering.  
* Avoid race conditions related to applications with the same name, but different uid (e.g. an application is deleted then quickly recreated with different content)  
* To fit in with the existing architecture as much as possible.

**Architectural significant constraints:**

* (The following refer to principal and managed agent, but the same applies in the reverse direction for autonomous agent and principal)  
* Principal has no direct visibility into managed agent Applications (for example, K8s access to agent cluster), instead it must send/receive messages  
* Principal doesn’t know what it missed in control plane CRs, while principal is offline  
* Principal doesn't know when managed agent OS process has restarted, and vice versa  
  * (This is something we actually could support, via persistence to a custom resource, or persistent volume. I have an example of this below)  
* An Application  .spec can be created, modified, deleted while the principal OS process is stopped.  
* (Currently) no persistent state for agent or principal  
  * (This is something we could support, via persistence to a CR, or persistent volume. I have an example of this below.)  
* An especially challenging case is the case where the OS processes of both managed agent and principal restart at same time, thus losing all memory resident state.

## New Message Types

See ‘example workflow’ section below for how this works in practice.

**‘request-basic-entity-list’: a request from peer (e.g agent) to source of truth (e.g. principal) to send list of (name, uuid) for all known entities (e.g. applications)**

* message parameter: sha256 checksum (`[]byte`)  
  * parameter is a checksum of the known universe from peer (e.g. managed agent); if checksum matches on both sides, then no additional work is required, since both are already in sync.  
* Sent from managed agent \-\> principal, or principal \-\> autonomous agent  
* Sent from peer (e.g. managed agent) to source of truth (e.g. principal) on OS process startup.  
* Source of truth receives this message, and sends **basic-entity** responses: one for each entity (e.g. application) in the namespace  
* Why: along with **basic-entity** allows us to handle these cases:  
  * A) handle the case where managed agent is connecting for the first time (has never connected before)  
  * B) handle the case where applications were deleted by the user, on the agent side, while the agent process was offline. (challenging case A)  
  * C) detect applications that were created on principal side while managed agent was offline (this is the least useful of the 3, as this is also handled elsewhere)   
* **Note:** replaces **‘hello**’ message which existed in v1 version of document

**‘basic-entity’: this is the reply to ‘request-basic-entity-list’ (above), sent from source of truth (e.g. principal) to peer (e.g. managed agent), and contains simple (name, uuid) pair for a given entity (e.g. application)**

* message parameters: entity name (string), entity uid (string)  
* Sent from principal \-\> managed agent, or autonomous agent \-\> principal  
* Message contains only the name and uid of an entity (e.g. application).   
* The primary purpose of this message is to communicate that an entity with a given name/uid exists, to the receiver.  
  * The receiver will decide if detailed contents are required, which can be acquired via **request-update**.  
  * There is intentionally no checksum included in this message: the goal of this part of the algorithm is only identifying missing applications on peer side (e.g. managed agent); applications that exist on both but are out-of-sync will be handled elsewhere.  
* Why:  
  * See **‘request-basic-entity-list**’ above.  
* **Note:** The term "*basic*" here just means, 'send us ***only*** the name/uuid of application, ***not*** the contents or checksum’  
  * Rather than 'basic', could instead use 'header', like 'request-entity-header-list', or other suggestions welcome (‘simple’?).  
* **Note:** replaces **‘liveness-check-response**’ message which existed in v1 of document

**‘request-update’: sent from peer (e.g. managed agent) to source of truth (e.g. principal), and requests the latest contents of a resource (e.g. application)**

* message parameters: name (string), uuid (string), checksum of entity (`[]byte`)  
  * The *checksum* parameter is the checksum of the entity contents (e.g. application .spec) from the peer (e.g. managed agent). If the checksum matches, no response is needed (because they are in sync).  
* Sent from managed agent \-\> principal, or principal \-\> autonomous agent  
* When received, source of truth (e.g. principal) checks if the entity with the given (name, uid) exists:   
  * if it exists, it send a SpecUpdate containing the new content  
    * BUT, if it exists, and the checksum matches, then no spec update needs to be sent.  
  * If it doesn’t exist, send a Delete indicating that the entity has been deleted.  
* Why? \- Allows us to detect:  
  * A) applications that exist on both sides, but are now out of sync. Useful for the case where a user modified an application on the agent side, while the agent was offline. (challenging case A)  
  * B) applications that no longer exist on principal, and should be deleted from managed agent (are orphaned). This is challenging case B.  
* **Note:** replaces **‘liveness-check-request’** and **‘liveness-check-response**’ message which existed in v1

**'request-entity-resync': informs the peer (e.g. managed agent) that it may be out of sync, and request the peer (e.g. managed agent) to send a ‘request-update' message for each entity (e.g. application) that the peer (e.g. managed agent) knows about.**

* no parameters.  
* Sent from principal \-\> managed agent, or autonomous agent \-\> principal.  
* **Request-entity-resync** informs the peer that they (may) need to resync. This is sent because the source of truth was (temporarily) offline, and thus may have missed some entities (e.g. application) being deleted.  
* Why?:  
  * Allows us to detect orphaned applications (applications that were deleted from principal while the principal was offline, and thus should be deleted from the agent). This is challenging case B.  
  * Allows us to detect .spec updates that were made while the principal was offline (though this is less useful, as this will also be caught elsewhere)  
* **Note:** Name change from v1, was previously called **liveness-resync**.

## Example Workflows

**A) Managed agent connects to principal (on agent OS process start):**

**Summary:** On managed agent OS process startup, principal sends a simple list of known applications to the agent. Agent requests any that are missing. The agent requests the latest version of all the applications *it* has. Principal replies with any that are out of sync/deleted. (Checksumming is used in both cases to reduce data transfer when they are already in sync.)

In this example, the agent starts up, connects to the principal, and performs a resync. This is only required once on agent startup.

Note that all these APIs/actions are asynchronous. At no point should a go-routine be blocked waiting for a response. All steps are fire and forget (this was true as well for v1, but I want to mention it here explicitly)

* 1\) Agent looks at the Argo CD Applications in its namespace, and produces a SHA256 checksum of the **(application name, uuid)** list contents of all of those Applications.  
* 2\) Agent sends **'request-basic-entity-list'** to principal with that checksum.  
* 3\) Principal receives **'request-basic-entity-list'**,   
  * It likewise produces a SHA256 checksum of its own **(application name, uuid)** list of applications, and compares them to agent checksum  
    * If the checksums match, the agent/principal state is the same, so no further action is needed. Stop here.  
  * On checksum mismatch, continue…  
  * For each Application on principal side, principal sends a ‘**basic-entity**' message to the agent which contains the **(application name, uuid)** list  
  * Why?  
    * This is an improvement from v1 since we don't need to send spec for every application, just name/uuid  
    * checksum is an optimization for the best case scenario: in the best case scenario, nothing has changed between agent/principal.  
* 4\) Agent receives **‘basic-entity’** messages sent by previous step  
  * Compares **(application name, application uid)** of the Application with its corresponding local Application  
  * If an Application with that name AND UID is not found, agent sends **request-update (name, uuid, checksum)** (where checksum is empty).  
* 5\) Principal receives **‘request-update’** message sent by previous step  
  * Principal looks for a corresponding Application with that name/uid.  
  * If Application with name/uuid exists: send a **SpecUpdate** message with the latest contents of that Application   
  * If no Application with that name/uuid exists: Send **Delete** message with the name/uid

*Occurring at the same time as 1-5, that is, in parallel:*

* 6\) Agent looks at the Argo CD Applications in its namespace, and sends **request-update** for each application.  
  * The **request-update** message includes the name, uuid, and checksum (of the contents) of the Application.  
    * Why?   
      * This is our mechanism for ensuring that each Application on the agent side is up to date with the latest contents from the principal.   
      * We use checksum to avoid having to send the contents if it matches.  
* 7\) Principal receives **‘request-update’** message sent by previous step  
  * Principal compares the checksum from the agent with the checksum from the principals version of the application; on match, do nothing.  
  * If there is no match: send a SpecUpdate message with the latest contents of that Application (or send Delete if the application with that uid no longer exists)

**B) Principal restart (on principal OS process start):**

**Summary:** On principal restart, principal sends a message informing the agent that the principal OS process restarted. Agent responds by requesting the latest version of all applications that it (Agent) knows about. Checksumming is used to reduce bandwidth.

In this example, the principal OS process (re)starts up, and thus must trigger a resync in the corresponding agent. This is only required once on principal startup.

The goal of this workflow is that the principal needs to inform the agent that the agent is potentially out of sync. The agent, on being told it is out of sync, will then send its requests for updates of its current state. The principal will reply with its current state, and the agent will resolve the difference.

* 1\) On initial startup of principal, principal sends **request-entity-resync** message to managed agent.  
* 2\) Managed agent receives **request-entity-resync**, and sends a **request-update** for each  application that that managed-agent knows about  
  * So if a managed-agent has 50 applications in its namespace, it will send 50 request-updates.  
* 3\) Principal responds to **request-update** as above.  
  * TL;DR:   
    * if name/uuid/checksum matches, do nothing.   
    * If checksum doesn’t match, send updatespec or delete depending on whether the application exists on the principal side.

Why?

* Allows us to detect orphaned applications (applications that were deleted from principal while the agent was offline), challenging case B.  
* Allows us to detect .spec updates that were made while the principal was offline (although this is less useful as it is detected elsewhere in the process)

### Other Principal and Agent Changes

**We should communicate the UID of the principal's Argo CD Application, from principal to managed agent (and vice versa for autonomous):**

* As part of Create/Update/Delete/UpdateSpec/etc messages, we should include the Application UID from the source of truth (source of truth is principal for managed agent case, autonomous agent for autonomous agent case)  
* Likewise we should communicate the UID of autonomous agents Application, from autonomous agent to principal.

**We should store the principal's Application uid as an annotation on the managed agent's Application (and vice versa for autonomous):**

* On the managed agent side, we should add an annotation like 'application-uid: (uid from principal)'.  
* This annotation will be used by the algorithm to avoid race conditions. See examples elsewhere.  
* Likewise with the autonomous/principal case.           	   
* See FAQ for why.

**Outbound message queue (outbox) should never stop trying to send messages:**

* In order to avoid race conditions, the agent and the principal should never discard messages.  
* They should not stop trying to send messages.  
* Unsent messages in the outbox should be constantly retransmitted on an exponential backoff.  
* This is covered further, here: [https://github.com/argoproj-labs/argocd-agent/issues/117](https://github.com/argoproj-labs/argocd-agent/issues/117)  
* I mention it here because it is necessary for the above algorithm to work

An example of a bad behaviour is [here](https://github.com/argoproj-labs/argocd-agent/blob/c372fcdf9f71e740af737becfa25ef11d86fd5f3/principal/callbacks.go#L80).

An example of why discarding messages is bad:

1. Managed agent connects to principal  
2. User creates Argo CD Application 'loan-app' in a principal Namespace  
3. Principal \-\> Agent message, informing agent that a new Application 'loan-app' was created.  
4. Agent creates Argo CD Application 'loan-app' in Argo CD namespace.  
5. (Connection is lost between managed agent and principal)  
6. User deletes Argo CD Application 'loan-app' in a principal Namespace  
   1. (or user updates app, or user creates a new app, the problem is the same)  
7. Principal attempts to send a delete message to the agent, but no agent is connected, so the message is discarded.  
8. (Agent successfully reconnects to principal)  
9. (BUT, since Agent missed the delete message, Agent still has the Application 'loan-app' in its namespace, and it will never be deleted)

How this would work with the above algorithm:

* ( … )  
* 7\) Principal attempts to send a delete message to the agent, but no agent is connected, so the message is kept in the queue, and queued to be resent.  
* 8\) (Agent successfully reconnects to principal)  
* 9\) Since the message is still in the queue, it is resent, and the agent receives it and deletes the application.

**Messages should only be removed from the outbound message queue once they are acknowledged as received, AND acknowledged as processed, by the recipient**

* For example, for a message: ‘application A created on principal’.   
* Principal should keep trying to send this message to the managed agent.  
* The principal should only stop once:  
* Agent has processed the message  
* Agent has sent a response to the principal indicating that the message has been processed.

## FAQ


### Why does the principal request that the agent inform it of all known resources (e.g. Applications) on startup?

This ensures that any changes that were made to Applications while the principal process was offline are detected and communicated to the agent.

### Why do the events use UIDs?

This guards against race conditions where messages are processed out of order, and also ensures the lifecycle of resources matches on both source of truth, and peer.

#### Messages processed out of order

For example, a failing case without uids:

Messages are sent between managed agent and principal in this chronological order:
1. Managed-agent->principal, message 1: 'request-update' message: is app 'A' still alive?
2. principal->managed-agent, message 2: 'delete' message: no, app 'A' is not alive
3. principal->managed-agent, message 3: new app was just created: app 'A'

However, the agent processes the messages out of order (for example due to go routine scheduling, or network traffic conditions):
* Managed agent processes message 3, first: agent creates new app named ‘A'
* BUT, then agent processes message 2: it then the deletes app

Because the ordering of messages is not guaranteed, app 'a' should exist on the agent but doesn't.

Using the .metadata.uid field of the Argo CD Application K8s resource, taken from the source of truth (principal), allows us to avoid this.

Here is how how the same scenario works, when using UIDs:

Messages are sent between managed agent and principal in this chronological order:
1. agent->principal, message 1: 'request-update' message: is app 'A' with uid '1234' still alive?
2. principal->agent, message 2: 'delete' message: no, app 'A' with uid '1234' is not alive
3. principal->agent, message 3: new app was just created: app 'A’', with uid '4567'

Agent processed the message out of order, but the behaviour is still correct:
Agent processes message 3, first: Agent deletes app 'A' with old uid '1234', then creates new app named 'A' with uid '4567'
Then agent processes message 2: ignores message to delete app 'a' with uid '1234', because app with this name/uid no longer exists.


#### Confusion due to lifecycle mismatch

Using UIDs also allows us to accurately track the lifecycle of individual resources on K8s control plane. This ensures that when events are sent from principal <-> agent, and processed by peer, that it is easy for peer to know if the event refers to a current version of a resource, or an old version of a resource that no longer exists.

A quick example of this:
1. Application 'A' is created on principal, and synced to managed agent.
2. Principal container is stopped (for any reason, OOM, node drain, etc)
3. While stopped, user deletes Application 'A' on principal, and replaces it with an entirely different Application but with the same name 'A'.
4. Principal container starts
5. Principal container sends contents of new application 'A' to managed agent.
   * Without resource UID, managed-agent would think that the Application from step 1, and the Application from step 3, were the same Application.

Resource UID allows us to detect that delete event 3) occured on the managed-agent side, and thus delete and recreate the Application on managed-agent side to match the behaviour on principal.
* An example of a problem that would occur without this:
   * Managed-agent would send back .status field which was generated by the Application from step 1, but for the Application that was created in step 3, despite there being no relation between the two.

Accurate lifecycle tracking via UIDs allows us to easily avoid this entire class of problems.

See challenging case 'E' above, for a detailed example.
