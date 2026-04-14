terraform {
  backend "gcs" {
    bucket = "dfh-prod-tfstate"
    prefix = "music-generator/state"
  }
}
