cmake_minimum_required(VERSION 3.20)
project(net_protocols_cpp LANGUAGES CXX)

set(CMAKE_CXX_STANDARD 20)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_EXPORT_COMPILE_COMMANDS ON)

enable_testing()

add_subdirectory(gtp)
add_subdirectory(sctp)
add_subdirectory(pfcp)
add_subdirectory(asn1-per)
add_subdirectory(diameter)
add_subdirectory(bfd)
add_subdirectory(lpm-fib)
add_subdirectory(nat-conntrack)
