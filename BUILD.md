# Build and installation

## Requirements

- GitHub CLI (`gh`)
- An authenticated GitHub CLI session for private repositories
- Go 1.21 or later for source builds

## Install a released version

```bash
gh extension install hironeko/gh-trends
```

Run it with:

```bash
gh trends --help
```

Upgrade or remove it with:

```bash
gh extension upgrade trends
gh extension remove trends
```

## Local development installation

```bash
git clone git@github.com:hironeko/gh-trends.git
cd gh-trends
make install
gh trends --help
```

`make install` builds `gh-trends`, stages it under `local/gh-trends`,
and installs that directory as a local extension. Run `make install` again after
source changes.

## Tests

```bash
make test
```

## Releases

Push a semantic version tag. The release workflow uses GitHub's official
`cli/gh-extension-precompile` action to publish binaries recognized by
`gh extension install`.

```bash
git tag v0.1.0
git push origin v0.1.0
```
