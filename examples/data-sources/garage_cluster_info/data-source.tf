data "garage_cluster_info" "this" {}

output "garage_layout_version" {
  value = data.garage_cluster_info.this.layout_version
}
