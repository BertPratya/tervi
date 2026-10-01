# 12. Subagents and Voice Interaction

## 12.1. Scope only

Subagents and voice interaction are included in the intended product scope. This section records those commitments without choosing their detailed UX, architecture, data model, or implementation. Inclusion does not establish a first-release requirement; delivery phases will be decided separately.

## 12.2. Subagents

Support agent delegation and expose subagent activity, results, and available controls through the browser where the selected harness supports them. Preserve useful harness-native subagent capabilities under section [5.5](conversations.md#55-supporting-additional-harnesses) rather than limiting all integrations to their shared features.

Users should be able to understand delegated work and inspect its results through the platform. The exact presentation and controls remain to be designed. Support does not imply that every harness offers identical delegation features or that the platform will implement its own orchestration engine.

Deferred decisions include how delegation is initiated, how progress is presented, available intervention and cancellation controls, concurrency behavior, permissions, and any cross-harness delegation. Session relationships, event structures, and storage schemas are also deferred.

## 12.3. Voice interaction

Allow users to speak to the agent and receive spoken responses alongside text chat. Voice is an additional way to interact with the platform's agents, not a requirement to use voice for ordinary work.

Whether voice uses a harness's native capability, a separate platform integration, or a combination remains undecided. Do not assume every harness exposes voice through its supported integration interface. Clearly identify supported capabilities and integration gaps.

Deferred decisions include voice providers, transcription and speech generation, UI placement, transcript visibility and storage, interruption behavior, switching between voice and text, and interaction while agent or background work continues. No data structures or message-routing design are selected here.

## 12.4. Capability coverage and next scope discussion

Both features follow the principle of consistent common controls with access to harness-specific capabilities. The platform should explain unavailable or not-yet-integrated features rather than silently losing them.

The agreed scope is to include subagents and voice interaction. Detailed design will be addressed later, without treating the deferred implementation choices as exclusions from the product.

Installation and technology decisions are recorded in section [13](deployment.md). Detailed subagent and voice implementation remains deferred.

