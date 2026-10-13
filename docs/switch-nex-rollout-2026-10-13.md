# Switch NEX sequential rollout

All 29 running Switch NEX titles are deployed with shared endpoint leases and
mandatory ticketed login. Every title passed health, running binary hash,
integration startup marker, and a live same-IP two-player probe: advertised
secure scheme, reverse-order ticket spending, independent candidate registration,
and flat peer URL replies. Mario Kart and Animal Crossing now have owner-confirmed gameplay; Animal Crossing flight passed after the ACBAA fix.
Native relay fallback remains unresolved and is not advertised.

| Title | Deployment | Owner gameplay |
| --- | --- | --- |
| castle-crashers-remastered | Passed | Pending |
| nobody-saves-the-world | Passed | Pending |
| pico-park | Passed | Pending |
| monopoly | Passed | Pending |
| duck-game | Passed | Pending |
| heroes-of-hammerwatch | Passed | Owner confirmed works |
| full-metal-furies | Passed | Owner confirmed works |
| mario-party-superstars | Passed | Owner reports simultaneous searches split lobbies; searching again joined. Human/CPU character slots then disagreed between clients. Bounded opening wait, unlocked dispatch between wait steps and creator participant count correction deployed. Full race/vet, socket same-room/count test and production login probe passed; simultaneous-search and roster acceptance pending. |
| mario-golf-super-rush | Passed | Pass revoked: owner subsequently reported Harbor ROG3 crash during Mario Golf; crash logs show GPU-thread SIGABRT from uncaught VK_ERROR_OUT_OF_DEVICE_MEMORY in TextureCache::JoinImages during a blit. Underlying allocation-exhaustion cause unresolved. Match entry works. Earlier PC run had countdown drift and 3–5 FPS, slowing the Switch; PC-specific cause remains unconfirmed. |
| arms | Passed | Combat works. Initial retry seemed good, then 2-AABQA-1020 recurred on Harbor Android; unresolved after key-preservation correction. |
| mario-kart-8-deluxe | Passed | Completed Switch + Harbor race |
| super-mario-bros-35 | Passed | Pending |
| mario-tennis-aces | Passed | Owner confirmed works |
| animal-crossing-new-horizons | Shared integration restored | Rollback baseline also failed; recovered flight contract and binding fix deployed. Captured rejected request passed live replay; owner confirmed flight worked after the binding fix. |
| luigis-mansion-3 | Passed | Pending |
| mario-strikers-battle-league | Passed | Harbor ROG3 crash: Android records SIGSEGV shortly after video-decoder stream startup; no native backtrace. Earlier texture assertions preceded Strikers launch and are not attributed to this title. Different signature from Golf’s Vulkan allocation abort; cause unresolved. |
| minecraft | Passed | Pending |
| splatoon-2 | Passed | Pending |
| super-mario-maker-2 | Passed | Pending |
| super-mario-odyssey | Passed | Pending |
| super-smash-bros-ultimate | Passed | Pending |
| super-mario-3d-world-bowsers-fury | Passed | Pending |
| kirbys-dream-buffet | Passed | Owner confirmed works |
| clubhouse-games-51 | Passed | Random matchmaking works; friend join fails. Explicit-join recap correction deployed; full race/socket test and live login probe pass. Owner confirmed friend-lobby retest passed. |
| risk-of-rain | Passed | Pending |
| streets-of-rage-4 | Passed | Pending |
| risk-of-rain-returns | Passed | Pending |
| dont-starve-together | Passed | Owner confirmed works |
| risk-of-rain-2 | Passed | Pending |

Owner handles game tests while deployments proceed sequentially. Duck Game's
first attempt rolled back due to a duplicate existing connectivity token mount;
after correcting replacement of existing mounts/environment, deployment and
live probe passed. Existing settings and base images were preserved and pinned.

Per-title releases, tokens, original quadlets, manifests, and guarded rollback
scripts reside privately under `~/openpak/deploy/connectivity/releases/` on
production. Mario Kart uses `20261013-switch-nex`; other titles use
`20261013-switch-<title>`. Full race suite and vet passed before rollout.
