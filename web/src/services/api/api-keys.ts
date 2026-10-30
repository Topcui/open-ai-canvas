import { http } from "./request";

export type UserAPIKeySource = "manual" | "login";

export interface UserAPIKey {
    id: string;
    userId: string;
    name: string;
    keyPrefix: string;
    keyLast4: string;
    source: UserAPIKeySource;
    expiresAt?: string;
    lastUsedAt?: string;
    createdAt: string;
    updatedAt: string;
}

export interface UserAPIKeyList {
    keys: UserAPIKey[];
    limit: number;
}

/** 新建结果；secret 只在这次响应里出现，服务端不再保存明文。 */
export interface CreatedUserAPIKey {
    key: UserAPIKey;
    secret: string;
}

export function listUserAPIKeys(): Promise<UserAPIKeyList> {
    return http.get<UserAPIKeyList>("/api-keys").then((result) => ({
        keys: Array.isArray(result?.keys) ? result.keys : [],
        limit: Number(result?.limit) || 0,
    }));
}

export function createUserAPIKey(name: string) {
    return http.post<CreatedUserAPIKey>("/api-keys", { name });
}

export function deleteUserAPIKey(id: string) {
    return http.delete<{ deleted: boolean }>(`/api-keys/${encodeURIComponent(id)}`);
}
