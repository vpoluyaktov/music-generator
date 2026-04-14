terraform {
  backend "gcs" {
    bucket = "dfh-stage-tfstate"
    prefix = "music-generator/state"
  }
}
