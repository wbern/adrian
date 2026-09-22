# Changelog

## [0.3.1](https://github.com/wbern/adrian/compare/v0.3.0...v0.3.1) (2026-09-22)


### Bug Fixes

* preserve multi-successor ADR graphs ([f744be6](https://github.com/wbern/adrian/commit/f744be68617b6b7670d45ccc1db3606feb68a938))
* preserve multi-successor ADR graphs ([4d12ab9](https://github.com/wbern/adrian/commit/4d12ab94b030b79c1f2892303e236f6f4d92d68a))

## [0.3.0](https://github.com/wbern/adrian/compare/v0.2.1...v0.3.0) (2026-09-22)


### Features

* evolve adr-lint into ADRian review planning ([96169e7](https://github.com/wbern/adrian/commit/96169e7d130dd969bb598d011dab111a5c3534a9))
* evolve adr-lint into ADRian review planning ([f3e349d](https://github.com/wbern/adrian/commit/f3e349df22936a2cfd7cf559ee60f9994dfb604b))

## [0.2.1](https://github.com/wbern/adrian/compare/v0.2.0...v0.2.1) (2026-09-22)


### Bug Fixes

* **claude:** capture stderr on the error path, and keep the prompt off argv ([6f1d2cc](https://github.com/wbern/adrian/commit/6f1d2cca7bfe620aa5bae8a4763cee60aa300f79))
* **claude:** send the prompt on stdin — argv dies at MAX_ARG_STRLEN on Linux ([ea95dbb](https://github.com/wbern/adrian/commit/ea95dbb7033dbf43750d5a7106d459f43a129d02))

## [0.2.0](https://github.com/wbern/adr-lint/compare/v0.1.3...v0.2.0) (2026-08-19)


### Features

* **cli:** check a supplied diff, report real cost, and stop reporting skips as passes ([#8](https://github.com/wbern/adr-lint/issues/8)) ([684bce8](https://github.com/wbern/adr-lint/commit/684bce83c0bcf20f0ba5a5bfe4cc30d3e895e4f2))

## [0.1.3](https://github.com/wbern/adr-lint/compare/v0.1.2...v0.1.3) (2026-05-13)


### Bug Fixes

* **hooks:** make adr-lint pre-commit on by default, add skip env var ([d35c718](https://github.com/wbern/adr-lint/commit/d35c718bc7a52b0e99b06047bfe8624f5ea845f3))

## [0.1.2](https://github.com/wbern/adr-lint/compare/v0.1.1...v0.1.2) (2026-05-13)


### Bug Fixes

* **release-please:** hide docs and refactor from changelog ([bea9110](https://github.com/wbern/adr-lint/commit/bea911085ad61a86ef9f31bf36e8465eb8b0e9d4))

## [0.1.1](https://github.com/wbern/adr-lint/compare/v0.1.0...v0.1.1) (2026-05-13)


### Documentation

* **adr:** add ADRs for release pipeline and local-only dogfooding ([d7a132e](https://github.com/wbern/adr-lint/commit/d7a132e9ba911bd092a2b8bb2223c2ba47924df1))

## 0.1.0 (2026-05-13)


### Features

* add list, show, deprecate, and supersede subcommands ([622e3b7](https://github.com/wbern/adr-lint/commit/622e3b7c04cfbdc271a3e71b0c434777ae394a8f))
* add validate, accept/reject/withdraw, and on-disk template ([077ef4f](https://github.com/wbern/adr-lint/commit/077ef4fbbc906c9562866df8cee7d69a64a691f0))
* add version subcommand and arity checks ([2147bfe](https://github.com/wbern/adr-lint/commit/2147bfe953bdfb467419d902834b049a1e788b1e))
* **cli:** add help and unknown-subcommand handling ([f8c70d4](https://github.com/wbern/adr-lint/commit/f8c70d41049c0a1b3df3421dd9098ec5c78d02b6))
* **cli:** add per-subcommand --help and guard self-supersession ([7d55857](https://github.com/wbern/adr-lint/commit/7d558577f4ee30f56f302271cdb768605ceaf00e))
* **cli:** print cwd-relative paths in status messages ([e1ac321](https://github.com/wbern/adr-lint/commit/e1ac3213371529d37110ba2214327d545176f6a7))
* **create:** add create subcommand and seed first ADR ([ba683bc](https://github.com/wbern/adr-lint/commit/ba683bc44bc1e7ddc02376f7088cb9ca54e3e6a9))
* parse superseded_by and surface it in list output ([c9404b7](https://github.com/wbern/adr-lint/commit/c9404b7bb8eccc6d032565cba5b9270183e4c94d))
* **validate:** collect all issues and detect malformed frontmatter ([138664d](https://github.com/wbern/adr-lint/commit/138664d352024516c04d9186d01907d6dbd56247))
* **version:** support build-time version injection via ldflags ([5a22a86](https://github.com/wbern/adr-lint/commit/5a22a86f54513fd37297cfba8674533b00a31d10))


### Bug Fixes

* make ADR writes atomic and create race-free ([47bbd6e](https://github.com/wbern/adr-lint/commit/47bbd6eeb2e824d00af42ea9b5060b183a842057))


### Refactoring

* harden subcommand dispatch and ADR status helpers ([7fb14aa](https://github.com/wbern/adr-lint/commit/7fb14aaee107610a1b49d39fdc8677119e9feeb7))


### Documentation

* add CONTRIBUTING.md with setup and commit conventions ([484b2a7](https://github.com/wbern/adr-lint/commit/484b2a7953c0febe9783498a698c509515bf2b86))
* add recorded demos and restructure README ([eb08ef1](https://github.com/wbern/adr-lint/commit/eb08ef186f68fa6bfe4559dec3aa36876c5a6919))
