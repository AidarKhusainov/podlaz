.PHONY: test vet deb package-inspect

test:
	go test ./...

vet:
	go vet ./...

deb:
	bash scripts/build-deb.sh

package-inspect: deb
	@package_version="$${PODLAZ_DEB_VERSION:-$${PODLAZ_VERSION:-0.0.0~dev}-1}"; \
	arch="$${PODLAZ_DEB_ARCH:-amd64}"; \
	dpkg-deb --info "dist/podlaz_$${package_version}_linux_$${arch}.deb"; \
	dpkg-deb --contents "dist/podlaz_$${package_version}_linux_$${arch}.deb"
