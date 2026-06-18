.PHONY: test vet deb package-inspect

test:
	go test ./...

vet:
	go vet ./...

deb:
	bash scripts/build-deb.sh

package-inspect: deb
	dpkg-deb --info dist/podlaz_$${PODLAZ_VERSION:-0.0.0~dev}_linux_$${PODLAZ_DEB_ARCH:-amd64}.deb
	dpkg-deb --contents dist/podlaz_$${PODLAZ_VERSION:-0.0.0~dev}_linux_$${PODLAZ_DEB_ARCH:-amd64}.deb
