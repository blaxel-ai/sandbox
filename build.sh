kraft pkg \
	--arch x86_64 \
	--plat kraftcloud \
	--name index.unikraft.io/blaxel/$1 \
	--rootfs Dockerfile.$1 \
	--runtime index.unikraft.io/official/base-compat:latest \
	--push \
	.
