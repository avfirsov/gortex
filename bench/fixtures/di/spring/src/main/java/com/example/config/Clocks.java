package com.example.config;

import java.time.Clock;
import org.springframework.context.annotation.Bean;
import org.springframework.context.annotation.Configuration;

// @Configuration + @Bean: factory-method style provider. Consumers
// that @Autowire a Clock field receive whatever this method returns.
// Statically the binding between the return type (Clock) and the
// producing method (systemClock) is visible only inside this class.
@Configuration
public class Clocks {
    @Bean
    public Clock systemClock() {
        return Clock.systemUTC();
    }
}
