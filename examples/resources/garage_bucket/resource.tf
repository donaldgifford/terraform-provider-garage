resource "garage_bucket" "example" {
  global_aliases = ["images.example.com"]

  # Optional quotas; omit to leave unbounded.
  max_size    = 10737418240 # 10 GiB
  max_objects = 1000000

  # When true, `terraform destroy` empties the bucket via the S3 data plane
  # before deleting it. Requires the provider's s3_endpoint / s3_access_key /
  # s3_secret_key to be configured (or their GARAGE_S3_* env equivalents).
  force_destroy = false
}
