package com.example.users;

import java.util.HashMap;
import java.util.Map;
import java.util.Optional;
import org.springframework.stereotype.Repository;

// Second implementation. Without @Primary on one of the two, a plain
// @Autowired UserRepository field is ambiguous — Spring requires a
// @Qualifier to pick between them.
@Repository("inMemoryUsers")
public class InMemoryUserRepository implements UserRepository {
    private final Map<String, User> users = new HashMap<>();

    public Optional<User> findById(String id) {
        return Optional.ofNullable(users.get(id));
    }

    public User save(User u) {
        users.put(u.getId(), u);
        return u;
    }
}
