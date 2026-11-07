add_library(diameter STATIC
    src/header.cpp
    src/avp.cpp
    src/message.cpp
    src/dictionary.cpp
    src/peer.cpp
)
target_include_directories(diameter PUBLIC include)
target_compile_options(diameter PRIVATE -Wall -Wextra)

add_executable(diameter_peer app/diameter_peer.cpp)
target_link_libraries(diameter_peer PRIVATE diameter)

add_executable(diameter_test tests/diameter_test.cpp)
target_link_libraries(diameter_test PRIVATE diameter)
add_test(NAME diameter COMMAND diameter_test)
