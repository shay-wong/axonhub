# Codex model catalog snapshot

These files are vendored unchanged from [openai/codex, tag `rust-v0.153.4`](https://github.com/openai/codex/tree/rust-v0.153.4), under the accompanying Apache 2.0 license:

| Local file | Upstream path | SHA-256 |
| --- | --- | --- |
| `models.json` | `codex-rs/models-manager/models.json` | `d7136a413cfac1b5b1686d9e0dcc5c80ca05bebed5e9fc3911376561d0ef6ee8` |
| `prompt.md` | `codex-rs/models-manager/prompt.md` | `ac8ae107a0d72fe3476b430afb161ea4e67da2e446d778aefc44828160559807` |
| `LICENSE` | `LICENSE` | `d17f227e4df5da1600391338865ce0f3055211760a36688f816941d58232d8dc` |
| `NOTICE` | `NOTICE` | `9d71575ecfd9a843fc1677b0efb08053c6ba9fd686a0de1a6f5382fd3c220915` |

The snapshot contains 11 models. Preserve complete model metadata, including `model_messages`: Codex replaces matching catalog entries wholesale, and a missing `model_messages.instructions_template` produces empty model instructions in this client version. The catalog context window is the Codex default; `max_context_window` is the ceiling for user overrides, not its replacement.

The adapter preserves the client's longest-prefix and single provider-namespace lookup from `codex-rs/models-manager/src/manager.rs`. It keeps the requested slug and sets `visibility` to `list` for models already authorized by the gateway, without mutating the shared snapshot. The fallback prompt comes from the same release. Unknown-model defaults are defined upstream in `codex-rs/models-manager/src/model_info.rs`, function `model_info_from_slug`; do not borrow another model's capabilities or instructions beyond the client's lookup rules. A positive configured model-card context limit overrides the generic fallback context.

To update, choose an explicit released Codex tag and retrieve these four files from that tag. Check the corresponding `ModelInfo` contract in `codex-rs/protocol/src/openai_models.rs`, catalog replacement logic in `codex-rs/models-manager/src/manager.rs`, and unknown-model defaults before updating the adapter. Preserve upstream file bytes, refresh the tag and hashes above, and run the focused catalog and models-handler tests. This snapshot is independent of AxonHub's general model/pricing catalog sync.
