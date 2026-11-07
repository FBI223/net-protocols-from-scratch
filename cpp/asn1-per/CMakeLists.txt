add_library(asn1-per STATIC
    src/bit_reader.cpp
    src/constraints.cpp
    src/types.cpp
    src/decoder.cpp
)
target_include_directories(asn1-per PUBLIC include)
target_compile_options(asn1-per PRIVATE -Wall -Wextra)

add_executable(per_decode app/per_decode.cpp)
target_link_libraries(per_decode PRIVATE asn1-per)

add_executable(asn1_per_test tests/asn1_per_test.cpp)
target_link_libraries(asn1_per_test PRIVATE asn1-per)
add_test(NAME asn1-per COMMAND asn1_per_test)
