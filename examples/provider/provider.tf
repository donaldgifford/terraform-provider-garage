provider "garage" {
  endpoint = "https://garage.example.com:3903"

  # The token attribute is omitted here; set the GARAGE_TOKEN environment
  # variable instead so the value never appears in plan output or state.
}
