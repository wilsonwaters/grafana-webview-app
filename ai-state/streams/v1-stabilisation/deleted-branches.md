# Deleted remote branches (GH1 #143, 2026-10-19)

Branches whose PRs were merged, or that matched `main`, were deleted from GitHub. To restore one: `git push origin <sha>:refs/heads/<branch>`, or use **Restore branch** on its PR page.

| Branch | Head SHA | PR |
|---|---|---|
| `claude/105-allowprivateip-wirethrough` | `f47d976adcb5699ad1e46a69f63adccf43cae09a` | #111 |
| `claude/123-nested-panel-logo` | `09e3459f8984d46b76ffbf5dd3493657420d56e9` | #130 |
| `claude/df1-backend-availability-hook` | `00a275b2ad91c830166abfddb9ac1fa6cd9a5e11` | #107 |
| `claude/df2-editor-degradation` | `23386a1460441f25eaaf217a06653e0ea60c6965` | #108 |
| `claude/df3-view-mode-guard` | `cd2952b9d3c948c6e83f950c3429aee7b7a890f5` | #109 |
| `claude/dr1-repo-docs` | `cd58fef1cc4398bc71e9daf4672fc3ebc45d90dd` | #126 |
| `claude/dr2-developer-guide` | `476f87bfb3b74905fe519e9a476300dd8820aa1e` | #125 |
| `claude/dr3-configuration-guide` | `100e09d1966476a56e71411fbe5e59c9c2e4ba3a` | #134 |
| `claude/dr4-administration-guide` | `2a3c296a6717419bd66ec00955e863fbc0a5fd84` | #128 |
| `claude/dr5-threat-model-architecture` | `fe1c7a93c838dd087a6282151861b9a3c058b642` | #127 |
| `claude/dr6-install-troubleshooting` | `9cd1cba125d86a6d9170213052d7c3ebc624049b` | #132 |
| `claude/dr7-readme-example-dashboard` | `bf44da85fba3363f7e96216feef830bb22133450` | #135 |
| `claude/dr8-v0.0.1-release-prep` | `42fdb8f42cf7e101445bd56bc33b05ec40454e10` | #136 |
| `claude/fix-release-workflow` | `c5c95a3e6d7209913dc9e0bb56ecb512fe7ea8cb` | #137 |
| `claude/grafana-webview-orchestrator-vtdg41` | `8e65e5803ed67ba6a69c296d847ea8d5c24264d7` | none (identical to main) |
| `claude/grafana-webview-plugin-setup-gzlH9` | `c91c6b1fa4dc7b97ef62a699eb92c3bfa1ba8982` | #61 |
| `claude/levitate-advisory-temp` | `ae9ece57bcb14f7b9b28af7a554e045e8917d309` | #124 |
| `claude/tc1-security-suite-ac17-22` | `4f29c134db85e3799920da6e499fed97675bf0de` | #104 |
| `claude/tc2-security-suite-ac23-29` | `2d516d25bd76149157a7361ae381800e15449a5c` | #103 |
| `claude/tc3-frontend-test-coverage` | `ce52568e6811b4b6ab19887b986620309bb0046e` | #110 |
| `claude/tc4-e2e-suite` | `82bc3b23d0333724282d2a7448dcbaba5aa2717c` | #112 |
| `claude/tc5-nonskippable-security-gate` | `ae34d6f8eb3e166003c9d13bdb5c705233129a71` | #106 |
| `claude/tc6-release-validator` | `d5efc528c920db42b824bd8791dd73f6610caf6f` | #122 |
| `cr1-gzip-html-detection` | `18d92622235a5dfff806afc2ee4daeba227df7e7` | #93 |
| `cr2-html-rewriting` | `a9d104b2db131be99bd3ce20fc81f6c05f5afc7a` | #94 |
| `cr3-proxy-resource` | `e72d0ead16cd004fe045ab803bfb4f88cc17cb7c` | #95 |
| `cr4-redirect-handling` | `259bf872659bbbf871b59513826582d084c0f078` | #96 |
| `cr5-hide-selectors` | `75f0d860ad9b89621def70278ef0ab5d05730be7` | #97 |
| `feat/11-panel-options-type` | `4ef24356cc202b0d4e0f059e69a50d85c0a5b42d` | #72 |
| `feat/12-settings-schema` | `75dcf36f89ac4163cc4be29b271916535824bcc9` | #73 |
| `feat/13-nested-panel` | `e48bd146315e69a82d86e9638d7f837dcf7b449e` | #74 |
| `feat/14-view-mode-viewport` | `df586207087372ce9f5a4f65d7e45412bb38c8d0` | #75 |
| `feat/15-viewport-interaction-math` | `32e0d5abe16b02c95b76b50f2118b9c061d230c0` | #76 |
| `feat/16-interactive-editor` | `8c538117b36de05324e47a3e14df6895a937f06a` | #77 |
| `feat/17-editor-inputs` | `0ca92ea5e829a35726ce53ec05ce62ccff7f4c72` | #79 |
| `feat/18-view-behaviours` | `c025fb901eda62b1f92e8e0598778aca9afc3c46` | #80 |
| `feat/19-ip-blocklist` | `5dbf4da0667f04ed07e62ab6e2c7b55662b6f7de` | #81 |
| `fix/ci-compat-multimodule` | `bccbce3cc96a70970c3ccd8af14be9a8280c720e` | #78 |
| `fr1-check-frameable` | `6cb81069bd88c4193c7acea367fcf02ea3185257` | #99 |
| `fr2-health` | `349333e8fa47eec6c2288d8b44ef29ed440a1282` | #98 |
| `fr3-test-url-button` | `18311c33f04f97f901f610c652ea2c7da2d95a75` | #100 |
| `fr4-load-mode-wiring` | `8224711bc33ce2c1ac633f1ae424ee9fc82d4c45` | #101 |
| `p1-core-proxy` | `e2d268bd0bd9ecfe1bd17c3b0b2ec29be2578866` | #86 |
| `p2-request-header-stripping` | `f640d4d22902e0f2385d450eed3729f57bc4b6a5` | #87 |
| `p3-response-header-stripping` | `ed4a3ed603190691ed22e6851dfe1bf69f7742d6` | #88 |
| `p4-resource-limits` | `ca56c3500fc2babe972f74a28d46fc8fa660d12d` | #89 |
| `p5-audit-logging` | `29a77c6f5666bb203073137607b688adeba9ec0b` | #90 |
| `p6-prometheus-metrics` | `5b36fe65b63aed55a3ecbc7686c9073e81be950b` | #91 |
| `p7-denial-wiring` | `431dc170380530a6559f8c35e08009e2d81aa096` | #92 |
| `sf2-url-validator` | `7c779065cfa9b1743e99b050a4468484ff8fc38e` | #82 |
| `sf3-allowlist-matcher` | `cd19a8f788f826c802323863c32d0a93c80ec84d` | #83 |
| `sf4-resolve-then-dial` | `14588176d7826ca8cf2a3893dee270313109ebbc` | #84 |
| `sf5-rate-limiter` | `b28636cbc425dd34dc5cc501dc71d06e1e51df47` | #85 |
