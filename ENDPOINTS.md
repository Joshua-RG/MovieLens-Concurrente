# 📘 Documentación API - Sistema de Recomendación Distribuido

## Información General

| Propiedad | Valor |
|-----------|-------|
| **Base URL** | `http://34.176.227.38:8080` |
| **Protocolo** | HTTP / JSON |

---

## 🔐 1. Autenticación y Seguridad

El sistema utiliza **JWT (JSON Web Tokens)**. Excepto por el Login y Registro, **todas** las peticiones deben incluir el token en la cabecera.

### Header Obligatorio

```text
Authorization: Bearer <TU_TOKEN_AQUI>
```

---

## 👤 2. Gestión de Usuarios

### 2.1 Registro de Nuevo Usuario

Crea un usuario nuevo en la base de datos MongoDB. El sistema generará un `user_id` único automáticamente.

| Detalle | Valor |
|---------|-------|
| **Método** | `POST` |
| **Endpoint** | `/register` |

#### Request

```json
{
    "username": "JuanPerez",
    "password": "mypassword123"
}
```

#### Response (201 Created)

```json
{
    "message": "Registro exitoso",
    "user_id": "1732345678",
    "username": "JuanPerez"
}
```

> **Nota:** El Frontend debe guardar este `user_id` si se desea hacer login automático o referencias futuras.

---

### 2.2 Iniciar Sesión (Login)

Soporta dos tipos de usuarios:
- **Usuarios Nuevos:** Usan su username y password creados en el registro.
- **Usuarios del Dataset (Legacy):** Usan su ID numérico (ej. "1") como usuario y como contraseña.

| Detalle | Valor |
|---------|-------|
| **Método** | `POST` |
| **Endpoint** | `/login` |

#### Request

```json
{
    "user_id": "JuanPerez",
    "password": "mypassword123"
}
```

> El `user_id` puede ser el username o el ID numérico del dataset.

#### Response (200 OK)

```json
{
    "token": "eyJhbGciOiJIUzI1Ni...",
    "user_name": "JuanPerez"
}
```

> **Nota:** Guardar el token en `localStorage` para futuras peticiones.

---

## 🎬 3. Catálogo y Películas

### 3.1 Obtener Películas (Catálogo)

Consulta directa a MongoDB. Soporta paginación y búsqueda por texto.

| Detalle | Valor |
|---------|-------|
| **Método** | `GET` |
| **Endpoint** | `/movies` |
| **Autenticación** | Requerida (Bearer Token) |

#### Parámetros (Query Params)

| Parámetro | Tipo | Descripción | Ejemplo |
|-----------|------|-------------|---------|
| `limit` | `integer` | Cantidad de resultados (default: 20) | `10` |
| `skip` | `integer` | Cuántos saltar para paginación (ej. página 2: skip=20) | `0` |
| `q` | `string` | Texto para buscar por título (Opcional) | `Toy` |

#### Ejemplo de Solicitud

```
GET /movies?limit=10&skip=0&q=Toy
```

#### Response

```json
{
    "count": 5,
    "page": 1,
    "movies": [
        {
            "id": "1",
            "title": "Toy Story (1995)",
            "genres": "Animation|Children's"
        }
    ]
}
```

---

## 🧠 4. Core: Recomendaciones (Distribuido)

Este es el proceso pesado. La API consulta a Redis primero; si no está, coordina con el Clúster de Workers vía TCP.

### 4.1 Obtener Recomendaciones

| Detalle | Valor |
|---------|-------|
| **Método** | `GET` |
| **Endpoint** | `/recommend` |
| **Autenticación** | Requerida (Bearer Token) |

#### Parámetros

| Parámetro | Tipo | Descripción | Ejemplo |
|-----------|------|-------------|---------|
| `user_id` | `string` | El ID del usuario conectado | `1` |
| `genre` | `string` | Filtrar por género (Opcional) | `Animation` |

#### Ejemplo de Solicitud

```
GET /recommend?user_id=1&genre=Animation
```

#### Response

```json
{
    "source": "Distributed Cluster",
    "processing_time": "1.5s",
    "filter_used": "Animation",
    "recommendations": [
        {
            "movie_id": "1",
            "title": "Toy Story (1995)",
            "score": 0.98
        },
        {
            "movie_id": "34",
            "title": "Babe (1995)",
            "score": 0.95
        }
    ]
}
```

> **Nota:** `source` puede ser `"Distributed Cluster"` o `"Cache (Redis)"` si es rápido.

---

## ⭐ 5. Interacción del Usuario

### 5.1 Calificar una Película

Guarda el rating en MongoDB y borra la caché de Redis para ese usuario (para que las próximas recomendaciones se recalculen con la nueva información).

| Detalle | Valor |
|---------|-------|
| **Método** | `POST` |
| **Endpoint** | `/rate` |
| **Autenticación** | Requerida (Bearer Token) |

#### Request

```json
{
    "user_id": "1",
    "movie_id": "1",
    "score": 5.0
}
```

| Campo | Tipo | Descripción |
|-------|------|-------------|
| `user_id` | `string` | ID del usuario |
| `movie_id` | `string` | ID de la película |
| `score` | `number` | Puntuación decimal entre 0.5 y 5.0 |

#### Response

```json
{
    "message": "Calificación guardada exitosamente."
}
```

---

### 5.2 Ver Historial (Mis Películas)

Muestra las últimas 20 películas que el usuario ha calificado, ordenadas por puntaje.

| Detalle | Valor |
|---------|-------|
| **Método** | `GET` |
| **Endpoint** | `/history` |
| **Autenticación** | Requerida (Bearer Token) |

#### Parámetros

| Parámetro | Tipo | Descripción |
|-----------|------|-------------|
| `user_id` | `string` | ID del usuario |

#### Response

```json
[
    {
        "movie_id": "1",
        "title": "Toy Story (1995)",
        "score": 5
    }
]
```

---

## ⚙️ 6. Panel de Administrador

### 6.1 Métricas del Sistema

Muestra el estado de salud de la API y el Clúster. Útil para el Dashboard de Admin.

| Detalle | Valor |
|---------|-------|
| **Método** | `GET` |
| **Endpoint** | `/stats` |
| **Autenticación** | Requerida (Bearer Token) |

#### Response

```json
{
    "active_workers": 3,
    "cpu_cores_api": 2,
    "goroutines_api": 8,
    "memory_usage_mb": 15
}
```

| Campo | Descripción |
|-------|-------------|
| `active_workers` | Nodos procesando actualmente |
| `cpu_cores_api` | Núcleos disponibles para la API |
| `goroutines_api` | Hilos ligeros activos |
| `memory_usage_mb` | Consumo RAM de la API |

---

## 💡 Notas de Arquitectura para el Frontend

### Latencia Variable

- **Primera vez:** Un usuario que pide recomendaciones puede tardar ~**1.5 segundos** (Cálculo distribuido en vivo)
- **Segunda vez:** Si no cambia filtros, tardará ~**10 milisegundos** (Caché Redis)

> **UX Tip:** Mostrar un Spinner o indicador de carga con el mensaje: *"Calculando recomendaciones para ti..."*

### Consistencia de Datos

- **Catálogo e Historial:** Provienen de MongoDB, siempre actualizados en tiempo real
- **Recomendaciones:** Se calculan en la RAM de los Workers

> **Nota:** Si un usuario nuevo se registra, podrá ver el catálogo y calificar, pero sus recomendaciones al principio se basarán en promedios o datos pre-cargados hasta que el sistema reinicie los workers y actualice su modelo en memoria.

---

## ⚠️ Manejo de Errores Comunes

| Código | Error | Causa | Acción |
|--------|-------|-------|--------|
| `401` | `Unauthorized` | El token expiró o no se envió correctamente | Redirigir a Login |
| `200` | `recommendations: []` | Usuario con gustos únicos o filtro muy restrictivo | Mostrar mensaje: "No encontramos coincidencias para este filtro" |