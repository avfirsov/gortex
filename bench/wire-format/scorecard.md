# GCX1 wire-format benchmark scorecard

## tiktoken cl100k_base (Claude 3 / Opus 4 / Sonnet 4 / Haiku 4.5 / GPT-4o family)

| case | tool | bytes (json) | bytes (gcx) | Δ% | tokens (json) | tokens (gcx) | Δ% | gzip (json) | gzip (gcx) | Δ% | round-trip |
|------|------|-------------:|------------:|---:|--------------:|-------------:|---:|------------:|-----------:|---:|:---------:|
| 01_search_symbols_small | search_symbols | 720 | 522 | −27.5% | 181 | 125 | −30.9% | 219 | 227 | +3.7% | ✓ |
| 02_search_symbols_large | search_symbols | 4201 | 2924 | −30.4% | 1068 | 742 | −30.5% | 850 | 823 | −3.2% | ✓ |
| 03_get_symbol_source_small | get_symbol_source | 731 | 731 | −0.0% | 257 | 255 | −0.8% | 359 | 372 | +3.6% | ✓ |
| 04_get_symbol_source_large | get_symbol_source | 1691 | 1663 | −1.7% | 534 | 532 | −0.4% | 678 | 689 | +1.6% | ✓ |
| 05_batch_symbols | batch_symbols | 1077 | 702 | −34.8% | 335 | 241 | −28.1% | 450 | 423 | −6.0% | ✓ |
| 06_find_usages_small | find_usages | 1287 | 989 | −23.2% | 335 | 255 | −23.9% | 339 | 327 | −3.5% | ✓ |
| 07_find_usages_large | find_usages | 1783 | 920 | −48.4% | 569 | 351 | −38.3% | 271 | 261 | −3.7% | ✓ |
| 08_get_file_summary | get_file_summary | 2172 | 1599 | −26.4% | 567 | 431 | −24.0% | 575 | 560 | −2.6% | ✓ |
| 09_analyze_hotspots | analyze_hotspots | 1731 | 1037 | −40.1% | 506 | 318 | −37.2% | 500 | 449 | −10.2% | ✓ |
| 10_analyze_dead_code | analyze_dead_code | 1132 | 825 | −27.1% | 288 | 198 | −31.2% | 365 | 355 | −2.7% | ✓ |
| 11_contracts_list | contracts | 2296 | 1562 | −32.0% | 580 | 463 | −20.2% | 630 | 608 | −3.5% | ✓ |
| 12_get_callers_medium | get_callers | 3021 | 2204 | −27.0% | 750 | 532 | −29.1% | 450 | 427 | −5.1% | ✓ |
| 13_smart_context | smart_context | 1816 | 1178 | −35.1% | 471 | 299 | −36.5% | 585 | 494 | −15.6% | ✓ |
| 14_get_dependents_small | get_dependents | 1269 | 929 | −26.8% | 329 | 239 | −27.4% | 318 | 307 | −3.5% | ✓ |
| 15_get_test_targets | get_test_targets | 1256 | 997 | −20.6% | 311 | 262 | −15.8% | 340 | 357 | +5.0% | ✓ |
| 16_find_implementations | find_implementations | 936 | 690 | −26.3% | 224 | 157 | −29.9% | 247 | 259 | +4.9% | ✓ |
| 17_find_cycles | analyze_cycles | 224 | 192 | −14.3% | 87 | 90 | +3.4% | 145 | 165 | +13.8% | ✓ |
| 18_graph_stats | graph_stats | 494 | 512 | +3.6% | 162 | 174 | +7.4% | 335 | 368 | +9.9% | ✓ |
| 19_get_editing_context | get_editing_context | 927 | 708 | −23.6% | 233 | 171 | −26.6% | 329 | 318 | −3.3% | ✓ |
| 20_get_repo_outline | get_repo_outline | 784 | 690 | −12.0% | 237 | 224 | −5.5% | 358 | 363 | +1.4% | ✓ |

**Summary (cl100k_base):** 20/20 cases. Median token savings: −27.4%. Median byte savings: −26.8%. Round-trip integrity: 20/20.

## Claude Opus 4.7 (estimated (×1.35 scalar over cl100k_base))

| case | tool | tokens (json) | tokens (gcx) | Δ% | source |
|------|------|--------------:|-------------:|---:|:------:|
| 01_search_symbols_small | search_symbols | 244 | 169 | −30.7% | est. |
| 02_search_symbols_large | search_symbols | 1442 | 1002 | −30.5% | est. |
| 03_get_symbol_source_small | get_symbol_source | 347 | 344 | −0.9% | est. |
| 04_get_symbol_source_large | get_symbol_source | 721 | 718 | −0.4% | est. |
| 05_batch_symbols | batch_symbols | 452 | 325 | −28.1% | est. |
| 06_find_usages_small | find_usages | 452 | 344 | −23.9% | est. |
| 07_find_usages_large | find_usages | 768 | 474 | −38.3% | est. |
| 08_get_file_summary | get_file_summary | 765 | 582 | −23.9% | est. |
| 09_analyze_hotspots | analyze_hotspots | 683 | 429 | −37.2% | est. |
| 10_analyze_dead_code | analyze_dead_code | 389 | 267 | −31.4% | est. |
| 11_contracts_list | contracts | 783 | 625 | −20.2% | est. |
| 12_get_callers_medium | get_callers | 1013 | 718 | −29.1% | est. |
| 13_smart_context | smart_context | 636 | 404 | −36.5% | est. |
| 14_get_dependents_small | get_dependents | 444 | 323 | −27.3% | est. |
| 15_get_test_targets | get_test_targets | 420 | 354 | −15.7% | est. |
| 16_find_implementations | find_implementations | 302 | 212 | −29.8% | est. |
| 17_find_cycles | analyze_cycles | 117 | 122 | +4.3% | est. |
| 18_graph_stats | graph_stats | 219 | 235 | +7.3% | est. |
| 19_get_editing_context | get_editing_context | 315 | 231 | −26.7% | est. |
| 20_get_repo_outline | get_repo_outline | 320 | 302 | −5.6% | est. |

**Summary (Opus 4.7):** 20/20 cases. Median token savings: −27.3%. Exact rows: 0/20 (rest estimated via ×1.35 scalar).
