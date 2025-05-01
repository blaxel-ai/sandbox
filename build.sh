if [ -z "$1" ]; then
	echo "Usage: $0 <sandbox>"
	exit 1
fi
if [ -z "$BL_ENV" ]; then
	BL_ENV="prod"
fi
if [ -z "$TAG" ]; then
	TAG="latest"
fi

UKC_URL="index.unikraft.io"
PREFIX="blaxel"

if [ "$BL_ENV" = "dev" ]; then
	PREFIX="$PREFIX/dev"
elif [ "$BL_ENV" = "prod" ]; then
	PREFIX="$PREFIX/prod"
fi

mkdir tmp || echo "tmp directory already exists"

# Read and update the JSON file
if [ -f "hub/$1.json" ]; then
    echo "Updating hub/$1.json with image information"
    jq --arg img "$PREFIX-$1:$TAG" '. + {"image": $img}' "hub/$1.json" > "tmp/$1.json.tmp"
else
    echo "Warning: hub/$1.json not found"
		exit 1
fi

echo $UKC_URL/$PREFIX/$1:$TAG
kraft pkg \
	--arch x86_64 \
	--plat kraftcloud \
	--name "$UKC_URL/$PREFIX-$1:$TAG" \
	--rootfs Dockerfile.$1 \
	--runtime $UKC_URL/official/base-compat:latest \
	--push \
	.

curl -X PUT -H "Content-Type: application/json" \
	-d @tmp/$1.json.tmp \
	$BL_API_URL/admin/store/sandbox/$1 \
	-u $BL_ADMIN_USERNAME:$BL_ADMIN_PASSWORD