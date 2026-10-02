// Command s3bucket ensures an S3 bucket exists; used by tests/run.sh before
// starting the enterprise server against MinIO.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

func main() {
	endpoint := flag.String("endpoint", "", "S3 endpoint, may include a scheme")
	bucket := flag.String("bucket", "nipa", "bucket name")
	accessKey := flag.String("access-key", "minioadmin", "access key id")
	secretKey := flag.String("secret-key", "minioadmin", "secret access key")
	flag.Parse()

	if *endpoint == "" {
		log.Fatal("s3bucket: -endpoint is required")
	}

	secure := strings.HasPrefix(*endpoint, "https://")
	host := strings.TrimPrefix(strings.TrimPrefix(*endpoint, "http://"), "https://")

	client, err := minio.New(host, &minio.Options{
		Creds:  credentials.NewStaticV4(*accessKey, *secretKey, ""),
		Secure: secure,
	})
	if err != nil {
		log.Fatalf("s3bucket: create client: %v", err)
	}

	ctx := context.Background()
	exists, err := client.BucketExists(ctx, *bucket)
	if err != nil {
		log.Fatalf("s3bucket: check bucket %q: %v", *bucket, err)
	}
	if exists {
		fmt.Printf("bucket %q already exists\n", *bucket)
		return
	}
	if err := client.MakeBucket(ctx, *bucket, minio.MakeBucketOptions{}); err != nil {
		log.Fatalf("s3bucket: create bucket %q: %v", *bucket, err)
	}
	fmt.Printf("bucket %q created\n", *bucket)
}
