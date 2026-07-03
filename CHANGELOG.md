# Changelog

## [1.5.0](https://github.com/tammersaleh/confluence-sync/compare/v1.4.0...v1.5.0) (2026-07-03)


### Features

* add ListChildren, GetAncestors, ListSpaces ([5809b3f](https://github.com/tammersaleh/confluence-sync/commit/5809b3ffe114b15068aba279a63ea90e8e5a0e04))
* add page children/ancestors/tree and space list ([9538007](https://github.com/tammersaleh/confluence-sync/commit/953800761b420b92296145ec991ff595cbff8651))


### Bug Fixes

* space_not_found hint no longer calls space list unavailable ([3d43fa8](https://github.com/tammersaleh/confluence-sync/commit/3d43fa8ff5984eadf6d680d85af95155dbb97b3c))

## [1.4.0](https://github.com/tammersaleh/confluence-sync/compare/v1.3.0...v1.4.0) (2026-07-03)


### Features

* add attachment list/download and space info ([1685551](https://github.com/tammersaleh/confluence-sync/commit/1685551e6a4938f3e6e57beb25c4547080463542))
* add GetSpaceByID and GetAttachmentByID, enrich Space ([d3c4be3](https://github.com/tammersaleh/confluence-sync/commit/d3c4be3f202b1b99c57d22c6106cdb369a65f0f1))

## [1.3.0](https://github.com/tammersaleh/confluence-sync/compare/v1.2.0...v1.3.0) (2026-07-03)


### Features

* add attachment URL resolver to the converter ([c93a2ba](https://github.com/tammersaleh/confluence-sync/commit/c93a2ba7ceaa11c422cbfac414b3e28640e816b4))
* add page get command (storage/adf/view/markdown) ([b181b40](https://github.com/tammersaleh/confluence-sync/commit/b181b40aaa2eb25047016a34ee2c5637c07876fa))
* add page list command ([410ca9a](https://github.com/tammersaleh/confluence-sync/commit/410ca9ac02df00a16b4e7f0529ff576f891eea19))

## [1.2.0](https://github.com/tammersaleh/confluence-sync/compare/v1.1.0...v1.2.0) (2026-07-03)


### Features

* add auth login, status, and logout ([767c890](https://github.com/tammersaleh/confluence-sync/commit/767c890f4a799b4a2b5673f7aedfe3d304624dab))
* add GetCurrentUser domain method ([d7b5af0](https://github.com/tammersaleh/confluence-sync/commit/d7b5af0c9ee1fed9802aa92879b02c579d1a5064))

## [1.1.0](https://github.com/tammersaleh/confluence-sync/compare/v1.0.0...v1.1.0) (2026-07-03)


### Features

* add confluenceurl parser ([35f3426](https://github.com/tammersaleh/confluence-sync/commit/35f3426487f464c6cea5dbc619389cb85f21553b))
* add ListPages and GetPage domain primitives ([d989a21](https://github.com/tammersaleh/confluence-sync/commit/d989a21356b3d0559bbd5e1588261ac0c0a2f181))
* typed transport errors, trace emission, rate-limit exit code ([570b415](https://github.com/tammersaleh/confluence-sync/commit/570b4156eb7f6b4472cc2e1238a2f56781c29933))


### Bug Fixes

* classify page_not_found and preserve error endpoint ([c90617c](https://github.com/tammersaleh/confluence-sync/commit/c90617c84fca4138265d369ce86545a99aa14f10))
* confluenceurl keeps port and accepts space overview URLs ([097b9b3](https://github.com/tammersaleh/confluence-sync/commit/097b9b3020e1ca73af15e0dea7c9d8e9dd2a65b1))

## 1.0.0 (2026-07-03)


### Features

* add credentials store ([9c12b09](https://github.com/tammersaleh/confluence-sync/commit/9c12b092d8f5fd064474b6627bea8339024914b9))
* add httpx tracer and generic error classification ([d450fe9](https://github.com/tammersaleh/confluence-sync/commit/d450fe9fa91a6b489510c1c50e9f36168c9309f7))
* add Kong CLI with version and space sync commands ([50982da](https://github.com/tammersaleh/confluence-sync/commit/50982daf1ec0782cf28fa67d1d19f785455bf4b2))
* port internal/output JSONL contract ([c4aa752](https://github.com/tammersaleh/confluence-sync/commit/c4aa75260d89438341c7ae9dabc579058850446e))
* replace --clean with --prune for surgical stale file removal ([2084ec1](https://github.com/tammersaleh/confluence-sync/commit/2084ec131c4d421395cfb5db5519a88b71cce9aa))


### Bug Fixes

* cancel commands cleanly on SIGINT/SIGTERM ([5fc7567](https://github.com/tammersaleh/confluence-sync/commit/5fc7567c21013cd26893452c281e372cc003cf9e))
* ensure deterministic file paths across sync runs ([9b6264a](https://github.com/tammersaleh/confluence-sync/commit/9b6264a75a994058c8d89fc6d9010d0889b0fae9))
* error when no site can be resolved ([972904c](https://github.com/tammersaleh/confluence-sync/commit/972904c30a265dee368239b981453768c187b9a6))
* honor --site and keep sync warnings visible under --quiet ([06dac0c](https://github.com/tammersaleh/confluence-sync/commit/06dac0cd375b9efc15454c3dfb41b370bacbeecb))
* prepend /wiki to webui URLs missing the prefix ([f01c378](https://github.com/tammersaleh/confluence-sync/commit/f01c378f105a1a24211a86cfce5d02c11a3278f7))
* resolve page hierarchy through non-page parents (databases, folders) ([085109a](https://github.com/tammersaleh/confluence-sync/commit/085109af37465b1f948e9724a0c8bc6b951eca0a))
* scope gitignore binary pattern to repo root ([bdcebe5](https://github.com/tammersaleh/confluence-sync/commit/bdcebe5c3726d1eb18c89af409c2a37347a46298))
* scope sync output-dir ignores to repo root ([827a2fe](https://github.com/tammersaleh/confluence-sync/commit/827a2fee71161cbe99991907f0782d7d7946cafa))
* skip broken attachments instead of aborting sync ([ff7a232](https://github.com/tammersaleh/confluence-sync/commit/ff7a23271d1604accd82049e711886d82a2accbd))

## Changelog
