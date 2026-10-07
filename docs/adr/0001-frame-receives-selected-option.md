# The content frame receives the selected option before Send

The shell sends the active question's content frame the option selected in 03, or the fact that a free-text answer exists, before the user sends feedback, so that diagrams, effect lines and prototypes in 02 follow the choice with one gesture. Round scripts can therefore read an unsent choice and, through the [accepted WebRTC egress](../../SECURITY.md#accepted-residual-risk-webrtc), transmit it. The frame never receives the free text, thread messages, screenshots, the shell capability or the round token.

Rejected: an option selector inside the frame only, which makes the user choose twice; diagrams drawn in the shell, which still leaves round prototypes unable to follow the choice.

Read this before sending any other shell state to the content frame, or before changing the WebRTC acceptance. The maintainer accepted the decision on 2026-10-06 ([grilling outcome](https://github.com/taekwondodev/lavagna/issues/10#issuecomment-6026564644), decision 9) and confirmed this record on 2026-10-07 ([frame ticket](https://github.com/taekwondodev/lavagna/issues/16)). The per-question page implements it ([#29](https://github.com/taekwondodev/lavagna/issues/29)).
