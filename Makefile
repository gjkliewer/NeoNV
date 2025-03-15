debug_run:
	NEONV_LOG_LEVEL=DEBUG go run -tags debug ./...

run:
	go run ./...

clean:
	rm -rf .tmp
	rm -rf NeoNV.app

package: clean
	go run fyne.io/fyne/v2/cmd/fyne package
	codesign --force --deep --sign - NeoNV.app # ad-hoc signature
	# cp foo.provisionProfile NeoNV.app/Contents
	# mv NeoNV.app .tmp

install:
	go get ./...
	go run fyne.io/fyne/v2/cmd/fyne install
