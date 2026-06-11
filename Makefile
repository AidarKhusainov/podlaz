.PHONY: test vet deb package-inspect

test:
	go test ./...

vet:
	go vet ./...

deb:
	./scripts/build-deb.sh

package-inspect: deb
	dpkg-deb --info dist/tunwarden_$${TUNWARDEN_VERSION:-0.0.0~dev}_$${TUNWARDEN_DEB_ARCH:-amd64}.deb
	dpkg-deb --contents dist/tunwarden_$${TUNWARDEN_VERSION:-0.0.0~dev}_$${TUNWARDEN_DEB_ARCH:-amd64}.deb
