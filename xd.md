Entendido. Si el enfoque es **100% demo sobre la aplicación**, el guion debe ser mucho más dinámico. Tienen que narrar la arquitectura *mientras* usan la interfaz, usando la aplicación como evidencia visual de lo que explican.

Aquí tienes el guion optimizado para **3 personas**, duración **5 a 6 minutos**, enfocado en vender la solución técnica mostrando solo el navegador web.

---

### 🎬 Guion de Demo: MovieLens Distributed Engine

**Configuración Inicial:**
* Tener la aplicación abierta en el navegador (Login).
* Tener la base de datos cargada y los workers listos.
* **Persona 1:** Comparte pantalla y maneja el mouse durante toda la demo (o se pasan el control, pero mejor uno solo para fluidez).

---

#### 🟢 Parte 1: Introducción y Acceso Seguro (Persona 1)
*(Tiempo: 0:00 - 1:30)*

**[Pantalla: Login Page]**

**Persona 1:**
"Buenos días, profesores. Somos el Grupo 4 y hoy les presentamos el **MovieLens Distributed Engine**. No hemos venido a mostrarles una simple página web, sino una solución de arquitectura distribuida diseñada para resolver un problema de Big Data: procesar 25 millones de interacciones en tiempo real."

**Persona 1:**
"Nuestra aplicación resuelve el problema de la 'sobrecarga de información' mediante un sistema de recomendación inteligente. Empecemos por el acceso."

**[Acción: Persona 1 escribe usuario y contraseña y hace clic en Login]**

**Persona 1:**
"Como ven, implementamos un sistema de autenticación completo. Al hacer login, el sistema no solo valida contra nuestra base de datos persistente, sino que genera un **Token JWT** que asegura que cada petición subsiguiente hacia nuestro clúster esté encriptada y autorizada. Aquí no hay sesiones monolíticas; todo es *stateless* y seguro."

**[Acción: Entra al Home/Catálogo y hace scroll rápido]**

**Persona 1:**
"Ya estamos dentro. Lo que ven aquí es el **Catálogo Global**. Esta vista está optimizada para lectura rápida, conectándose directamente a nuestra capa de persistencia en **MongoDB**. Esto nos permite navegar por miles de títulos sin afectar el rendimiento de los nodos de cálculo, que están reservados para la tarea pesada que mi compañero les mostrará a continuación."

---

#### 🟡 Parte 2: El Core Distribuido y Caché (Persona 2)
*(Tiempo: 1:30 - 3:30)*

**[Pantalla: Vista de Recomendaciones (vacía o inicial)]**

**Persona 2:**
"Gracias. Ahora vayamos al corazón del proyecto: el motor de recomendaciones. Aquí es donde la arquitectura distribuida brilla."

**[Acción: Persona 1 hace clic en 'Obtener Recomendaciones'. Se ve el spinner de carga por ~1 segundo]**

**Persona 2:**
"Fíjense en ese segundo de carga. Ese no es un retraso, es **cálculo puro**. En este momento, nuestra API ha actuado como Coordinador aplicando el patrón **Scatter-Gather**. Ha dividido la tarea y la ha enviado vía TCP a nuestro clúster de 3 nodos workers."

**[Acción: Aparecen los resultados]**

**Persona 2:**
"Ahí está. En tiempo real, el sistema ha comparado mi perfil contra miles de usuarios en memoria RAM, ha unificado los resultados parciales de los 3 nodos y me ha entregado estas películas personalizadas."

**[Acción: Persona 1 hace clic en 'Obtener Recomendaciones' OTRA VEZ inmediatamente]**

**Persona 2:**
"Ahora, miren esto. Hago clic de nuevo..." *(Los resultados salen instantáneos)* "...¡Instantáneo! Aquí entra en juego nuestra capa de **Caché con Redis**. El sistema detectó que ya hizo este trabajo pesado, aplicó el patrón *Cache-Aside* y nos devolvió el resultado en milisegundos, optimizando recursos."

**[Acción: Persona 1 escribe 'Comedy' en el filtro y busca]**

**Persona 2:**
"Finalmente, la flexibilidad. Si filtro por 'Comedia', el sistema inyecta este filtro en los nodos, que discriminan los vectores en memoria y me devuelven solo lo que quiero ver, demostrando que no son cálculos estáticos, sino dinámicos."

---

#### 🔵 Parte 3: Observabilidad y Arquitectura (Persona 3)
*(Tiempo: 3:30 - 5:30)*

**[Acción: Persona 1 navega a la pestaña 'Panel de Admin' / 'Dashboard']**

**Persona 3:**
"Todo lo que han visto es bonito, pero como ingenieros, necesitamos saber qué pasa tras bambalinas. Para eso construimos este **Dashboard de Observabilidad en Tiempo Real**."

**[Pantalla: Tabla de Contenedores con CPU/RAM en vivo]**

**Persona 3:**
"Este panel se conecta directamente al socket de Docker. Aquí está la prueba de nuestra arquitectura:"

1.  "Miren los componentes **Worker-1, Worker-2 y Worker-3**. Esto demuestra nuestra **Escalabilidad Horizontal**. No dependemos de un servidor gigante, sino de múltiples nodos trabajando en paralelo."
2.  "Fíjense en la columna de **Memoria (RAM)**. Cada nodo está consumiendo más de 2 GB. Esto valida nuestra estrategia **In-Memory**: cargamos el dataset de 25 millones al inicio para evitar la latencia de disco."
3.  "Y si miran la **CPU**, verán picos de actividad distribuida. Esto nos confirma que el balanceo de carga es efectivo; ningún nodo se queda ocioso mientras los otros trabajan."

**[Acción: Persona 1 puede ir a la pestaña 'Logs' y mostrar logs en vivo si los tienen en el front, si no, quedarse en la tabla]**

**Persona 3 (Cierre):**
"En conclusión, hemos logrado construir una solución que no solo funciona, sino que es **robusta y escalable**. Hemos pasado de un algoritmo local a una arquitectura de microservicios capaz de soportar alta demanda gracias a la combinación de **Go** para el cómputo, **Redis** para la velocidad y **Docker** para la orquestación. Muchas gracias."

---

### 💡 Consejos Clave para este formato:

1.  **Sincronización:** La Persona 1 (la que mueve el mouse) debe ser lenta y deliberada. No hacer clic hasta que el compañero lo anuncie.
2.  **Vender la "Espera":** Cuando cargue la recomendación por primera vez (ese 1 segundo de demora), no se queden callados. Usen ese segundo para decir: *"Justo ahora los 3 nodos están procesando..."*. Eso convierte una espera en una demostración de poder.
3.  **Panel de Admin:** Es su "As bajo la manga". Asegúrense de que se vean los datos (CPU/RAM) antes de empezar a grabar. Si salen en 0%, generen tráfico antes de entrar a esa pantalla.