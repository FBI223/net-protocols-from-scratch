# MQTT 3.1.1 / 5.0

**Specification:** OASIS specification  
**Verify with:** mosquitto_pub, mosquitto_sub

## What this is

A lightweight publish/subscribe protocol used for IoT.

## What to build

- Broker with CONNECT, PUBLISH and SUBSCRIBE
- Topic matching with wildcards
- QoS 0 and QoS 1
- Retained messages and keep-alive
- Test with mosquitto_pub and mosquitto_sub

## Design notes

_Package layout, key data structures and decisions._

## How it was tested

_Unit tests, captured packets in `testdata/`, fuzzing, and the real tools used._

## Benchmarks

_The exact command and the results._

## Limitations

_What is not implemented._
